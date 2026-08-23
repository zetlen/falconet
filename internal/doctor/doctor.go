// Package doctor is README "Install it in your repository" steps 1–8 as
// code: each step's Check: line, as a pure function over what the verb has
// already fetched or read. The verb itself, cmd/falconet/doctor.go, is the
// flags, the repository, the files, the API calls and the exit code; what it
// hands in here is the repository object, the permissions objects, the
// secret and label names, the config, the stack listing and the caller
// workflow's text, and what it gets back is lines.
//
// Nothing here touches the filesystem or the network. That is what lets
// every check be held to a table without a server, and the two things a
// table cannot exhaust — the report's column and the Actions pattern match
// — to properties.
//
// # Why this exists
//
// Setup was nine steps of `gh`, typed by hand (ADR-0006). The first consumer
// found five wiring bugs on its first canary, three of them fail-open, and
// the README's troubleshooting table is the catalogue: a caller that grants
// less than a job declares is a startup_failure with no jobs, no logs and
// nothing on the issue; a label that does not exist turns a hand-over into
// a failed step at precisely the moment falconet is trying to tell somebody
// something. Every one of those is a question with a mechanical answer, and
// a person should not have to be the one asking it.
//
// # The three words, and the fourth
//
// A check is `ok`, `MISSING`, or `cannot tell (why)`. `cannot tell` is not
// ok: a check that could not run has not passed, and the exit code says so.
// A `note` is not a check — it is something the README says in a sentence
// rather than a Check: line, such as the default token permission being
// read (fine: the caller grants what it needs) or a `uses:` ref that is
// still `main` — and it is counted nowhere.
//
// # Without a token
//
// ADR-0006 D4: setup's credential is FALCONET_SETUP_TOKEN and nothing else,
// and without one the verb "degrades to the README, never to nothing". So
// the local checks run, every remote check says `cannot tell (no
// FALCONET_SETUP_TOKEN)`, and the permission table is printed once on
// stderr, as the hint, with the README's advice to mint the token with a
// seven-day expiry. The issue body said a missing token was exit 2 with the
// table; that reading and this one conflict, and this is the one D4 means.
package doctor

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/zetlen/falconet/internal/github"
)

// Status is what a line says about its check.
type Status int

const (
	OK Status = iota
	Missing
	CannotTell
	Note
	// Done and Skipped are init's: a step this run did, and a step it did
	// not attempt and lists under "Left for you:". doctor, which writes
	// nothing, prints neither; the column fits both.
	Done
	Skipped
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Missing:
		return "MISSING"
	case CannotTell:
		return "cannot tell"
	case Note:
		return "note"
	case Done:
		return "done"
	case Skipped:
		return "skipped"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// Column is where the text starts on every line: the widest status word,
// "cannot tell", plus two spaces, so the step numbers line up down the page.
const Column = 13

// Line is one check, or one note, as the report prints it.
type Line struct {
	Status Status
	// Step is the README step, 1–8; 0 is a line about the run itself, such
	// as the token, and prints without a number.
	Step int
	Text string
	// Hint, when set, is printed indented on the next line: what to do
	// about a MISSING, in the README's own command.
	Hint string
}

// String is the line as the report prints it, without a trailing newline.
func (l Line) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s", Column, l.Status)
	if l.Step > 0 {
		fmt.Fprintf(&b, "%d. ", l.Step)
	}
	b.WriteString(l.Text)
	if l.Hint != "" {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", Column))
		b.WriteString(l.Hint)
	}
	return b.String()
}

// Report is every line, in step order.
type Report []Line

// Counts is how many checks are ok, missing, and could not be told. Notes
// are not checks and are not counted.
func (r Report) Counts() (ok, missing, cannotTell int) {
	for _, l := range r {
		switch l.Status {
		case OK:
			ok++
		case Missing:
			missing++
		case CannotTell:
			cannotTell++
		}
	}
	return
}

// Count is how many lines carry one status — init's summary counts more
// statuses than doctor's three.
func (r Report) Count(s Status) int {
	n := 0
	for _, l := range r {
		if l.Status == s {
			n++
		}
	}
	return n
}

// Summary is the last line: `doctor: N ok, M missing, K cannot tell`.
func (r Report) Summary() string {
	ok, missing, cannotTell := r.Counts()
	return fmt.Sprintf("doctor: %d ok, %d missing, %d cannot tell", ok, missing, cannotTell)
}

// Clean is whether every check is ok — the exit-0 condition. A check that
// could not be told has not passed.
func (r Report) Clean() bool {
	_, missing, cannotTell := r.Counts()
	return missing == 0 && cannotTell == 0
}

// --- lines that are about not being able to look ----------------------------

// NoToken is a remote check with no FALCONET_SETUP_TOKEN to make it with.
func NoToken(step int, text string) Line {
	return Line{Status: CannotTell, Step: step, Text: text + " (no FALCONET_SETUP_TOKEN)"}
}

// CannotTellWhy is a check that could not run, and why.
func CannotTellWhy(step int, text, why string) Line {
	return Line{Status: CannotTell, Step: step, Text: text + " (" + why + ")"}
}

// The fine-grained permission the reference lists for each endpoint doctor
// probes, named in a refusal so the operator knows what to add to the token.
const (
	NeedsMetadata       = "Metadata: read"
	NeedsAdministration = "Administration: read"
	NeedsSecrets        = "Secrets: read"
	NeedsIssues         = "Issues: read"
)

// Refused is a remote check whose call failed. A 403 or 404 is GitHub saying
// the token cannot see this — on a private repository the two are the same
// answer (ADR-0005) — so the line names the permission the endpoint needs.
// Any other status is quoted as it came. No response at all is the endpoint
// being unreachable, which the verb reports once on stderr as well.
func Refused(step int, text string, err error, needs string) Line {
	var e *github.Error
	if !errors.As(err, &e) {
		return CannotTellWhy(step, text, "GITHUB_API_URL unreachable")
	}
	why := fmt.Sprintf("%d %s", e.Status, e.Reason())
	if e.Status == http.StatusForbidden || e.Status == http.StatusNotFound {
		why += " — needs " + needs
	}
	return CannotTellWhy(step, text, why)
}

// ClassicToken reads X-OAuth-Scopes, which a classic token reports and a
// fine-grained one does not. A classic token needs `repo` (ADR-0006 D4);
// one without it gets a note naming the scopes it has, once, so that the
// refusals below it have an explanation above them. No header, or `repo`
// among the scopes, is no note.
func ClassicToken(scopes string) (Line, bool) {
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		return Line{}, false
	}
	for _, s := range strings.Split(scopes, ",") {
		if strings.TrimSpace(s) == "repo" {
			return Line{}, false
		}
	}
	return Line{Status: Note, Text: fmt.Sprintf("the token is classic and its scopes (%s) do not include repo, which a classic token needs", scopes)}, true
}

// TokenHint is what doctor prints on stderr, once, when there is no token:
// ADR-0006 D4's table, as it applies to both setup verbs, and the README's
// advice on minting. init prints the same table under its own first line,
// through TokenHintFor.
var TokenHint = TokenHintFor("doctor", "the remote checks cannot run")

// TokenHintFor is TokenHint for a verb, with what the missing token costs
// it.
func TokenHintFor(verb, consequence string) string {
	return fmt.Sprintf(tokenHint, verb, consequence)
}

const tokenHint = `%s: no FALCONET_SETUP_TOKEN in the environment, so %s.

Mint a fine-grained personal access token scoped to this one repository, with a
seven-day expiry, and export it as FALCONET_SETUP_TOKEN. doctor reads; init
writes, and the column says which level each needs:

  Permission       doctor   init    For
  Administration   read     read    step 1's actions/permissions checks
  Actions          read     read    the same
  Secrets          read     write   steps 3–5, the four secrets
  Issues           read     write   step 6's labels, and the canary

A classic token needs the repo scope. GITHUB_TOKEN and GH_TOKEN are deliberately
not read: in CI they are the Actions token, which cannot do this and must never
be asked to; on a laptop they are whatever someone set for something else.
`

// --- step 1: the repository qualifies ----------------------------------------

// Stack is what the verb found on disk for one configured stack.
type Stack struct {
	// Key is the config key the name came from: "plan" or "validate_only".
	Key  string
	Name string
	// IsDir is whether the name is a directory under the repository root.
	IsDir bool
	// TFFiles is how many `.tf` files it holds directly.
	TFFiles int
}

// Stacks is README step 1's first bullet and step 7's check: each configured
// stack is a directory with `.tf` in it. configFile is where the names came
// from, for the hint.
func Stacks(stacks []Stack, configFile string) []Line {
	if configFile == "" {
		configFile = ".github/falconet.json"
	}
	if len(stacks) == 0 {
		return []Line{{Status: Note, Step: 1, Text: "no stacks are configured (.stacks.plan and .stacks.validate_only are both empty), so nothing would be validated or planned"}}
	}
	var lines []Line
	for _, s := range stacks {
		subject := fmt.Sprintf("stack %s (.stacks.%s)", s.Name, s.Key)
		hint := fmt.Sprintf("set .stacks.%s in %s to the directories your OpenTofu stacks live in", s.Key, configFile)
		switch {
		case !s.IsDir:
			lines = append(lines, Line{Status: Missing, Step: 1, Text: subject + " is not a directory", Hint: hint})
		case s.TFFiles == 0:
			lines = append(lines, Line{Status: Missing, Step: 1, Text: subject + " has no .tf files", Hint: hint})
		default:
			lines = append(lines, Line{Status: OK, Step: 1, Text: subject + " is a directory with .tf files"})
		}
	}
	return lines
}

// Issues is `has_issues`: the queue is an issue, so a repository without
// issues has no way in.
func Issues(r *github.Repository) Line {
	if r.HasIssues {
		return Line{Status: OK, Step: 1, Text: "the repository has issues enabled"}
	}
	return Line{Status: Missing, Step: 1, Text: "the repository has issues disabled",
		Hint: "enable them: Settings → General → Features → Issues"}
}

// Action is one thing the workflow `uses:` — an action, or a reusable
// workflow when Path is set.
type Action struct {
	Owner, Repo, Path string
}

func (a Action) String() string {
	return a.Owner + "/" + a.Repo
}

// RequiredActions is README step 1's list — what a repository restricted
// to "selected" actions must allow for any of this to run. `actions/*`
// stands for the GitHub-owned actions falconet.yml uses, listed in
// githubOwned: it is covered by github_owned_allowed, or by patterns that
// admit each of them.
var RequiredActions = []Action{
	{"zetlen", "falconet", ".github/workflows/falconet.yml"},
	{"actions", "*", ""},
	{"opentofu", "setup-opentofu", ""},
	{"anthropics", "claude-code-action", ""},
}

var githubOwned = []Action{
	{"actions", "checkout", ""},
	{"actions", "upload-artifact", ""},
	{"actions", "download-artifact", ""},
	{"actions", "create-github-app-token", ""},
}

// ActionsPolicy is `allowed_actions`: `all`, or `selected` with every one of
// RequiredActions covered. sel is the selected-actions object, needed only
// when the policy is `selected`; the verb fetches it then and hands in nil
// otherwise.
func ActionsPolicy(p *github.ActionsPermissions, sel *github.SelectedActions) Line {
	hint := "allow them: Settings → Actions → General → Actions permissions — all actions, or selected with " +
		"zetlen/falconet, actions/*, opentofu/setup-opentofu and anthropics/claude-code-action"
	if !p.Enabled {
		return Line{Status: Missing, Step: 1, Text: "Actions are disabled for this repository", Hint: hint}
	}
	switch p.AllowedActions {
	case "all":
		return Line{Status: OK, Step: 1, Text: "allowed_actions is all"}
	case "local_only":
		return Line{Status: Missing, Step: 1,
			Text: "allowed_actions is local_only: workflows from outside the repository cannot run", Hint: hint}
	case "selected":
		if sel == nil {
			return CannotTellWhy(1, "allowed_actions is selected", "the selected-actions list was not read")
		}
		var uncovered []string
		for _, a := range RequiredActions {
			if !Covered(sel, a) {
				uncovered = append(uncovered, a.String())
			}
		}
		if len(uncovered) == 0 {
			return Line{Status: OK, Step: 1, Text: "allowed_actions is selected, covering zetlen/falconet, actions/*, opentofu/setup-opentofu and anthropics/claude-code-action"}
		}
		hint = "add them: Settings → Actions → General → Allow specified actions and reusable workflows"
		if sel.VerifiedAllowed {
			hint += " (verified_allowed is on, which may cover a verified creator; doctor cannot tell which creators are, so add the pattern to be sure)"
		}
		return Line{Status: Missing, Step: 1,
			Text: "allowed_actions is selected and does not cover " + strings.Join(uncovered, ", "), Hint: hint}
	}
	return CannotTellWhy(1, "allowed_actions is "+p.AllowedActions, "a value doctor does not know")
}

// Covered is whether a "selected" policy admits one required action. The
// GitHub-owned set is admitted by github_owned_allowed, or by patterns that
// admit each action in it; anything else by a pattern.
func Covered(sel *github.SelectedActions, a Action) bool {
	if a.Owner == "actions" && a.Repo == "*" {
		if sel.GithubOwnedAllowed {
			return true
		}
		for _, g := range githubOwned {
			if !anyCovers(sel.PatternsAllowed, g) {
				return false
			}
		}
		return true
	}
	return anyCovers(sel.PatternsAllowed, a)
}

func anyCovers(patterns []string, a Action) bool {
	for _, p := range patterns {
		if Covers(p, a) {
			return true
		}
	}
	return false
}

// Covers says whether one entry of patterns_allowed admits an action or
// reusable workflow.
//
// GitHub documents the entry as the same reference a workflow writes —
// OWNER/REPOSITORY@REF for an action, OWNER/REPOSITORY/PATH@REF for a
// reusable workflow — with `*` matching any run of characters, slashes and
// all; its own examples are `space-org*/*` and `*/octocat**@*`. Two things
// it does not say are assumed here, and both lean towards "covered": an
// entry with no `@` admits every ref, and an entry with one admits whatever
// ref it names (the operator pinned it on purpose; whether that pin matches
// the caller's is the caller workflow's business, step 8). Names compare
// case-insensitively, as GitHub's do. An entry naming OWNER/REPOSITORY
// alone also admits the reusable workflows under it: that is how the README
// lists zetlen/falconet.
func Covers(pattern string, a Action) bool {
	pattern = strings.TrimSpace(pattern)
	if i := strings.LastIndex(pattern, "@"); i >= 0 {
		pattern = pattern[:i]
	}
	if pattern == "" {
		return false
	}
	re := globRegexp(pattern)
	if re.MatchString(a.Owner + "/" + a.Repo) {
		return true
	}
	return a.Path != "" && re.MatchString(a.Owner+"/"+a.Repo+"/"+a.Path)
}

// globRegexp is the pattern anchored, `*` as any run of characters and
// everything else literal, case-insensitive.
func globRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, part := range strings.Split(pattern, "*") {
		b.WriteString(regexp.QuoteMeta(part))
		b.WriteString(".*")
	}
	s := strings.TrimSuffix(b.String(), ".*") + "$"
	return regexp.MustCompile(s)
}

// WorkflowPermissionsNote is `default_workflow_permissions`, which the
// README says is fine either way: step 8's caller grants what it needs
// explicitly. A note, never MISSING.
func WorkflowPermissionsNote(wp *github.WorkflowPermissions) Line {
	why := "fine: the caller workflow grants what it needs"
	if wp.DefaultWorkflowPermissions == "write" {
		why = "the caller workflow grants what it needs either way"
	}
	return Line{Status: Note, Step: 1, Text: fmt.Sprintf("default_workflow_permissions is %s (%s)", wp.DefaultWorkflowPermissions, why)}
}

// RunnersNote is README step 1's "Linux x64 runners": the action installs a
// linux_x64 gitleaks and checks its digest. A note, not a check — runs-on
// is the caller's input, and ubuntu-latest is its default.
func RunnersNote() Line {
	return Line{Status: Note, Step: 1, Text: "runners must be Linux x64 (not checked: runs-on is the caller's input, and ubuntu-latest is the default)"}
}

// --- step 2: the handoff directory is ignored --------------------------------

// HandoffIgnored is `git check-ignore -q <dir>/`: ignored is whether that
// exited 0.
func HandoffIgnored(dir string, ignored bool) Line {
	dir = strings.TrimSuffix(dir, "/") + "/"
	if ignored {
		return Line{Status: OK, Step: 2, Text: dir + " is gitignored"}
	}
	return Line{Status: Missing, Step: 2, Text: dir + " is not gitignored",
		Hint: fmt.Sprintf("printf '%s\\n' >> .gitignore   (or: falconet init)", dir)}
}

// HandoffOutside is a handoff_dir that resolves outside the repository:
// there is nothing to gitignore, and README step 2 is about the directory
// inside the tree.
func HandoffOutside(dir string) Line {
	return Line{Status: Note, Step: 2, Text: dir + " is outside the repository, so nothing needs ignoring"}
}

// --- steps 3–5: the secrets -------------------------------------------------

// Secret is one of the four, and the step it is stored in.
type Secret struct {
	Name string
	Step int
}

// Secrets is the four, in README order.
var Secrets = []Secret{
	{"FALCONET_APP_ID", 3},
	{"FALCONET_APP_PRIVATE_KEY", 3},
	{"ANTHROPIC_API_KEY", 4},
	{"FALCONET_PLAN_ENV", 5},
}

// SecretLines is steps 3–5: each secret exists by name. A value is never
// readable, and the line says so for the reader who wonders what was
// checked. FALCONET_PLAN_ENV absent with no planned stacks is a note —
// README step 5: "if every stack you plan needs no credentials at all, skip
// this step" — which needs the config to have parsed (stacksKnown).
func SecretLines(existing []string, plannedStacks int, stacksKnown bool) []Line {
	lines := make([]Line, 0, len(Secrets))
	for _, s := range Secrets {
		lines = append(lines, SecretLine(s, existing, plannedStacks, stacksKnown))
	}
	return lines
}

// SecretLine is one of SecretLines.
func SecretLine(s Secret, existing []string, plannedStacks int, stacksKnown bool) Line {
	subject := "secret " + s.Name
	switch {
	case set(existing)[s.Name]:
		return Line{Status: OK, Step: s.Step, Text: subject + " exists (a value can never be read back, so the name is the check)"}
	case s.Name == "FALCONET_PLAN_ENV" && stacksKnown && plannedStacks == 0:
		return Line{Status: Note, Step: s.Step, Text: subject + " is not set (no planned stacks, so no planning environment is needed)"}
	}
	return Line{Status: Missing, Step: s.Step, Text: subject,
		Hint: fmt.Sprintf("store it: gh secret set %s   (or: falconet init)", s.Name)}
}

// --- step 6: the four labels ------------------------------------------------

// Labels is the four from config, in README order: issue.queue_label,
// labels.needs_info, labels.human, labels.pr.
type Labels struct {
	Queue, NeedsInfo, Human, PR string
}

// Names is the four in order.
func (l Labels) Names() []string {
	return []string{l.Queue, l.NeedsInfo, l.Human, l.PR}
}

// LabelLines is step 6: each of the four exists, one line each, in the
// README's order. Two config keys naming one label is two lines about it.
func LabelLines(labels Labels, existing []string) []Line {
	lines := make([]Line, 0, 4)
	for _, name := range labels.Names() {
		lines = append(lines, LabelLine(name, existing))
	}
	return lines
}

// LabelLine is one of LabelLines.
func LabelLine(name string, existing []string) Line {
	subject := "label " + name
	if set(existing)[name] {
		return Line{Status: OK, Step: 6, Text: subject}
	}
	return Line{Status: Missing, Step: 6, Text: subject,
		Hint: fmt.Sprintf("create it: gh label create %s   (or: falconet init)", name)}
}

// --- step 7: the config ------------------------------------------------------

// ConfigLine is step 7's first check: the file parses. file is the path
// config.Load read, or empty when none was found and the defaults stand
// alone; err is config.Load's error, quoted as the MISSING line.
func ConfigLine(file string, err error) Line {
	if err != nil {
		return Line{Status: Missing, Step: 7, Text: fmt.Sprintf("the config does not parse: %q", err.Error()),
			Hint: "check it: jq -e . " + orDefault(file, ".github/falconet.json")}
	}
	if file == "" {
		return Line{Status: OK, Step: 7, Text: "no .github/falconet.json, so the defaults stand alone (they name the stacks this was extracted from)"}
	}
	return Line{Status: OK, Step: 7, Text: file + " parses"}
}

// KnownPrompts are the prompt names falconet reads, as config keys.
var KnownPrompts = []string{"implement", "pause_needs_info"}

// Prompt is one `prompts.*` key the config file sets, and what the verb
// found at the path it names.
type Prompt struct {
	Key, Path string
	// Inside is whether the path stays under the repository root — relative,
	// and not climbing out of it.
	Inside bool
	Exists bool
}

// PromptLines is step 7's other half: every `prompts.*` override names a
// file under the repository root that exists. Only the keys the file sets
// are checked — a default is the shipped prompt, which the binary carries
// (ADR-0006, #3) — and a key falconet does not read is a note, because
// config that names a prompt nothing asks for is dead, and the rename that
// made it so (#5, park → pause) is recent.
func PromptLines(prompts []Prompt) []Line {
	known := set(KnownPrompts)
	var lines []Line
	for _, p := range prompts {
		subject := fmt.Sprintf("prompts.%s names %s", p.Key, p.Path)
		switch {
		case !known[p.Key]:
			lines = append(lines, Line{Status: Note, Step: 7,
				Text: fmt.Sprintf("prompts.%s is not a prompt falconet reads (the two are %s)", p.Key, strings.Join(KnownPrompts, " and "))})
		case !p.Inside:
			lines = append(lines, Line{Status: Missing, Step: 7, Text: subject + ", which is not under the repository root",
				Hint: "a prompt path is relative to the repository root (README step 7)"})
		case !p.Exists:
			lines = append(lines, Line{Status: Missing, Step: 7, Text: subject + ", which does not exist",
				Hint: "copy the shipped prompt into the repository and point the key at the copy (README step 7)"})
		default:
			lines = append(lines, Line{Status: OK, Step: 7, Text: subject + ", which exists"})
		}
	}
	return lines
}

// --- step 8: the caller workflow -------------------------------------------

// WorkflowPath is where the caller lives, and Reusable is what it must use.
const (
	WorkflowPath = ".github/workflows/infra-requests.yml"
	Reusable     = "zetlen/falconet/.github/workflows/falconet.yml"
)

// Permission is one entry of a `permissions:` block.
type Permission struct {
	Scope, Level string
}

// RequiredPermissions is what the caller's top-level permissions: block
// must grant: the widest any job in falconet.yml declares — publish's
// `contents: write` to push, `issues: write` and `pull-requests: write` to
// open and label the pull request. A called workflow can only narrow the
// caller's token, never widen it, and the check happens when the file is
// LOADED: grant less and the run is a startup_failure with no jobs, no logs
// and nothing on the issue, which is the row the README's troubleshooting
// table opens with. contract.test.sh holds README step 8 to falconet.yml;
// this list is step 8's block, and drifts with it.
var RequiredPermissions = []Permission{
	{"contents", "write"},
	{"issues", "write"},
	{"pull-requests", "write"},
}

// Caller is what ParseCaller reads out of the workflow's text.
type Caller struct {
	// Ref is what follows `@` on the `uses:` line that names Reusable; empty
	// when no line does (HasUses says which).
	HasUses bool
	Ref     string
	// HasFalconetRef is whether the caller still passes a `falconet-ref:`
	// input. There is no such input since #19 — it chose which falconet the
	// jobs checked out into the consumer's tree, and the checkout is gone —
	// and a reusable workflow rejects an input it does not declare, at load.
	HasFalconetRef bool
	// HasPermissions is whether a top-level `permissions:` key exists at all.
	HasPermissions bool
	// Inline is a scalar value on that key — `write-all`, `read-all` — or
	// empty when the value is a block or a flow mapping.
	Inline string
	// Grants is the block's entries, in file order.
	Grants []Permission
}

// ParseCaller reads the caller the way contract.test.sh reads the README's:
// by lines and indentation, no YAML library. Comments are dropped, quotes
// around a value are removed, and a top-level key is one at column 0. It
// reads the `uses:` line naming Reusable wherever it is, a `falconet-ref:`
// key wherever it is, and the `permissions:` key at the top level only — a
// job-level block is the job's own business and GitHub checks it against
// the top-level one, not the other way round.
func ParseCaller(text []byte) Caller {
	var c Caller
	lines := strings.Split(string(text), "\n")
	inBlock := false
	for _, raw := range lines {
		indent, key, value, ok := yamlLine(raw)
		if !ok {
			continue
		}
		if inBlock {
			if indent == 0 {
				inBlock = false
			} else {
				if key != "" {
					c.Grants = append(c.Grants, Permission{key, value})
				}
				continue
			}
		}
		switch {
		case key == "uses" && strings.HasPrefix(value, Reusable+"@"):
			c.HasUses = true
			c.Ref = strings.TrimPrefix(value, Reusable+"@")
		case key == "falconet-ref":
			c.HasFalconetRef = true
		case key == "permissions" && indent == 0:
			c.HasPermissions = true
			switch {
			case value == "":
				inBlock = true
			case strings.HasPrefix(value, "{"):
				c.Grants = append(c.Grants, flowMapping(value)...)
			default:
				c.Inline = value
			}
		}
	}
	return c
}

// yamlLine is one line as `key: value`, with its indentation. Blank lines
// and comments are not lines (ok is false); a sequence item's `- ` is part
// of the indentation, so `- uses: x` reads as `uses`.
func yamlLine(raw string) (indent int, key, value string, ok bool) {
	trimmed := strings.TrimLeft(raw, " \t")
	indent = len(raw) - len(trimmed)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return 0, "", "", false
	}
	if strings.HasPrefix(trimmed, "- ") {
		trimmed = strings.TrimLeft(trimmed[2:], " ")
		indent += 2
	}
	if i := strings.Index(trimmed, " #"); i >= 0 {
		trimmed = trimmed[:i]
	}
	trimmed = strings.TrimRight(trimmed, " \t\r")
	key, value, found := strings.Cut(trimmed, ":")
	if !found {
		return indent, "", trimmed, true
	}
	return indent, strings.TrimSpace(key), unquote(strings.TrimSpace(value)), true
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// flowMapping reads `{contents: write, issues: write}`; `{}` is no grants.
func flowMapping(v string) []Permission {
	v = strings.TrimSuffix(strings.TrimPrefix(v, "{"), "}")
	var out []Permission
	for _, entry := range strings.Split(v, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(entry), ":")
		if !found || strings.TrimSpace(key) == "" {
			continue
		}
		out = append(out, Permission{strings.TrimSpace(key), unquote(strings.TrimSpace(value))})
	}
	return out
}

// level orders a grant: write admits read admits none.
func level(s string) int {
	switch s {
	case "write":
		return 2
	case "read":
		return 1
	}
	return 0
}

// WorkflowLines is step 8: the caller exists; a `uses:` line names the
// reusable workflow at some ref; it passes no input the workflow does not
// declare; the top-level permissions: block grants at least
// RequiredPermissions. Granting less is MISSING naming the permission;
// granting more is a note; a `main` ref is a note, not MISSING — the README
// says to pin once a canary has reached a pull request, and a canary needs
// the file to exist first.
func WorkflowLines(text []byte, exists bool) []Line {
	if !exists {
		return []Line{{Status: Missing, Step: 8, Text: WorkflowPath,
			Hint: "write it from README step 8   (or: falconet init)"}}
	}
	lines := []Line{{Status: OK, Step: 8, Text: WorkflowPath + " exists"}}
	c := ParseCaller(text)

	// The uses: line.
	switch {
	case !c.HasUses:
		lines = append(lines, Line{Status: Missing, Step: 8, Text: "no uses: line names " + Reusable,
			Hint: "jobs.falconet.uses: " + Reusable + "@<sha or tag>"})
	case c.Ref == "":
		lines = append(lines, Line{Status: Missing, Step: 8, Text: "the uses: line names " + Reusable + " with no ref after the @",
			Hint: "jobs.falconet.uses: " + Reusable + "@<sha or tag>"})
	default:
		lines = append(lines, Line{Status: OK, Step: 8, Text: "it uses " + Reusable + "@" + c.Ref})
		if c.Ref == "main" {
			lines = append(lines, Line{Status: Note, Step: 8, Text: "the ref is main: unpinned — pin a SHA or tag once a canary has reached a pull request"})
		}
	}

	// `falconet-ref` was an input until #19: it chose which falconet the
	// jobs checked out into the consumer's tree, and it went with that
	// checkout — the action at the tag the workflow is pinned to installs
	// the binary from that same tag, so the uses: ref is the one coordinate.
	// A reusable workflow REJECTS an input it does not declare, when the
	// caller's file is loaded: no jobs, no logs, nothing on the issue — the
	// row the README's troubleshooting opens with. So a caller still passing
	// it is MISSING and not a note, whatever the value; the note that used to
	// ask for the two refs to agree retired with the input.
	if c.HasFalconetRef {
		lines = append(lines, Line{Status: Missing, Step: 8, Text: "falconet-ref is no longer an input; remove it",
			Hint: "the run would be a startup_failure: a reusable workflow rejects an input it does not declare when the caller's file is loaded"})
	}

	// The permissions: block.
	startup := "the run would be a startup_failure: no jobs, no logs, nothing on the issue. Grant README step 8's permissions: block, verbatim"
	required := strings.Join(grantWords(RequiredPermissions), ", ")
	switch {
	case !c.HasPermissions:
		lines = append(lines, Line{Status: Missing, Step: 8, Text: "no top-level permissions: block, and falconet's widest job declares " + required, Hint: startup})
	case c.Inline == "write-all":
		lines = append(lines, Line{Status: OK, Step: 8, Text: "permissions: write-all grants " + required})
		lines = append(lines, Line{Status: Note, Step: 8, Text: "write-all grants every permission, which is more than falconet needs; step 8's three lines are enough"})
	case c.Inline != "":
		// read-all, or something else that is not a block: nothing here
		// reaches write.
		lines = append(lines, Line{Status: Missing, Step: 8,
			Text: fmt.Sprintf("permissions: %s grants none of %s", c.Inline, required), Hint: startup})
	default:
		got := map[string]string{}
		for _, g := range c.Grants {
			got[g.Scope] = g.Level
		}
		var short, extra []string
		for _, r := range RequiredPermissions {
			if level(got[r.Scope]) < level(r.Level) {
				short = append(short, r.Scope+": "+orDefault(got[r.Scope], "none"))
			}
		}
		for _, g := range c.Grants {
			if !isRequired(g.Scope) {
				extra = append(extra, g.Scope+": "+g.Level)
			}
		}
		if len(short) > 0 {
			lines = append(lines, Line{Status: Missing, Step: 8,
				Text: fmt.Sprintf("permissions grants %s, and falconet's widest job declares %s", strings.Join(short, ", "), required), Hint: startup})
		} else {
			lines = append(lines, Line{Status: OK, Step: 8, Text: "permissions grants " + required})
		}
		if len(extra) > 0 {
			lines = append(lines, Line{Status: Note, Step: 8, Text: "permissions also grants " + strings.Join(extra, ", ") + ", which nothing in falconet needs"})
		}
	}
	return lines
}

func isRequired(scope string) bool {
	for _, r := range RequiredPermissions {
		if r.Scope == scope {
			return true
		}
	}
	return false
}

func grantWords(ps []Permission) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Scope+": "+p.Level)
	}
	return out
}

// --- helpers ---------------------------------------------------------------

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// SortedKeys is the keys of a map in order, for the verb to walk the
// prompts map deterministically.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
