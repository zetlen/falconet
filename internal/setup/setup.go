// Package setup is what `falconet init` decides and what it writes, as pure
// functions: the labels to create, the stacks a repository holds and how the
// flags sort them, the bytes of each file, the commit message, and the words
// of what is left for a person. The verb itself, cmd/falconet/init.go, is the
// flags, the repository, the prompts, git, the API calls and the exit code;
// what it hands in here is what it read, and what it gets back is what to
// write.
//
// Nothing here touches the filesystem, the network or the environment:
// discovery walks an fs.FS the verb hands in, and every writer returns bytes.
// That is what lets each decision be held to a table, and the idempotence
// the issue asks for — a second run changes nothing — to a property.
//
// # Why init exists
//
// Setup was nine steps of `gh`, typed by hand, and the part of adopting
// falconet that cost an afternoon and produced the wiring bugs the README's
// troubleshooting table catalogues (ADR-0006). doctor (#9) turned each
// step's Check: line into a question with a mechanical answer; init turns
// each step's write into one too. Steps 2, 7 and 8 need no token (#10);
// steps 4, 5 and 6 need one (#11); step 3's App is registered by manifest
// from a browser, its two secrets sealed straight from GitHub's answer
// (#12, internal/appmanifest), or sealed from flags when a person
// registered it by hand.
//
// # Every read before any write, and the first write is the idempotent one
//
// ADR-0006 D4: classic tokens report their scopes in X-OAuth-Scopes, and
// fine-grained ones report nothing, so the only way to learn what a token
// can do is to try. init performs every read first, and its first write is
// the labels — idempotent and harmless — so a token short of `Secrets:
// write` fails there, before anything hard to undo has happened, with the
// missing permission named.
//
// # Committed, never pushed
//
// ADR-0006 D5: the .gitignore line, .github/falconet.json and the caller
// workflow are written and committed locally, and init prints the push and
// does not run it. Pushing a workflow file through the API needs the
// `workflow` scope, which the token does not have and should not; pushing
// over the operator's own git credentials needs nothing; and the last step
// staying in a person's hands is this project's shape. The tree must be
// clean first, for the reason prepare refuses a dirty one: the commit this
// verb makes must carry only what it wrote.
package setup

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/zetlen/falconet/internal/doctor"
	"github.com/zetlen/falconet/internal/github"
)

// --- step 6: the labels ----------------------------------------------------

// LabelStep is one of the four labels, in README order: what to call it,
// and the label to create when it does not exist — nil when it does, or
// when an earlier config key already named the same label (two keys naming
// one label is one label).
type LabelStep struct {
	Name   string
	Create *github.Label
}

// style is the colour and description each label is created with, keyed by
// the config key that names it. The descriptions are README step 6's
// "Applied by" column, so the label explains itself on the issue; the
// colours are GitHub's own palette — blue for the queue a person feeds,
// yellow for a question, red for a hand-over, purple for a pull request.
var style = map[string]struct{ Color, Description string }{
	"queue":      {"1d76db", "Queued for falconet: a person applies this to request a change"},
	"needs_info": {"fbca04", "falconet paused with a question for the requester"},
	"human":      {"d93f0b", "falconet paused: a person has to take this one over"},
	"pr":         {"5319e7", "Opened by falconet: the plan in the body needs review"},
}

// Labels is step 6 as a plan: for each of the four, whether to create it,
// given the labels ListLabels returned. A name that exists is not created;
// a name that is created once is not created twice.
func Labels(want doctor.Labels, existing []string) []LabelStep {
	have := make(map[string]bool, len(existing))
	for _, n := range existing {
		have[n] = true
	}
	keys := []string{"queue", "needs_info", "human", "pr"}
	steps := make([]LabelStep, 0, 4)
	for i, name := range want.Names() {
		step := LabelStep{Name: name}
		if !have[name] {
			s := style[keys[i]]
			step.Create = &github.Label{Name: name, Color: s.Color, Description: s.Description}
			have[name] = true
		}
		steps = append(steps, step)
	}
	return steps
}

// --- step 7: the stacks and the config -------------------------------------

// skipped are the directories discovery never enters: git's own, tofu's
// provider cache, and a JavaScript dependency tree that can hold anything —
// besides every dot-directory, which is somebody's tool's and not a stack.
var skipped = map[string]bool{".git": true, ".terraform": true, "node_modules": true}

// DiscoverStacks is every directory under the root that directly contains a
// .tf file, as a sorted list of root-relative paths. README step 1: each
// stack is its own subdirectory, so a .tf at the root itself is not a stack
// and is not counted — falconet runs `tofu -chdir=<stack>` and never
// touches the repository root.
func DiscoverStacks(fsys fs.FS) ([]string, error) {
	found := map[string]bool{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && (skipped[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if dir := path.Dir(p); dir != "." && strings.HasSuffix(d.Name(), ".tf") {
			found[dir] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	stacks := make([]string, 0, len(found))
	for s := range found {
		stacks = append(stacks, s)
	}
	sort.Strings(stacks)
	return stacks, nil
}

// Stacks is how the discovered stacks are sorted: README step 7's rule is
// that `plan` is every stack a human will apply from the pull request, and
// `validate_only` is every other directory with .tf in it.
type Stacks struct {
	Plan, ValidateOnly []string
}

// SplitList reads one comma-separated flag value: trimmed, cleaned of a
// trailing slash, empties dropped, in the order given.
func SplitList(flag string) []string {
	var out []string
	for _, s := range strings.Split(flag, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, path.Clean(s))
	}
	return out
}

// UnknownStackError is a name in --plan or --validate-only that discovery
// did not find: a usage error, since the flag is what is wrong.
type UnknownStackError struct {
	Flag, Name string
	Discovered []string
}

func (e *UnknownStackError) Error() string {
	return fmt.Sprintf("%s names %s, which is not a directory with .tf files in it (found: %s)",
		e.Flag, e.Name, strings.Join(e.Discovered, ", "))
}

// UnsortedError is a discovered stack that neither flag named. The verb
// resolves it: at a terminal it asks per stack; otherwise it files the stack
// under validate_only — the README's own rule for "every other directory
// with .tf in it", and the safe reading, since a validate-only stack is
// never planned and needs no credential — and prints a note naming it, so a
// person who meant --plan sees it. Guessing `plan` would plan a stack with
// no credentials; this error is the signal, not a refusal.
type UnsortedError struct {
	Names []string
}

func (e *UnsortedError) Error() string {
	return fmt.Sprintf("every stack must be named in --plan or --validate-only, and %s %s in neither",
		strings.Join(e.Names, ", "), is(len(e.Names)))
}

func is(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// Sort puts the discovered stacks into the two lists the flags name. Every
// discovered stack must be named in exactly one; a name in neither is an
// *UnsortedError, a name in both or in none of the discovered is an
// *UnknownStackError. Each list keeps the order the flag gave, without
// duplicates.
func Sort(discovered, plan, validateOnly []string) (Stacks, error) {
	known := make(map[string]bool, len(discovered))
	for _, s := range discovered {
		known[s] = true
	}
	placed := map[string]string{}
	var out Stacks
	for _, l := range []struct {
		flag  string
		names []string
		into  *[]string
	}{{"--plan", plan, &out.Plan}, {"--validate-only", validateOnly, &out.ValidateOnly}} {
		for _, n := range l.names {
			if !known[n] {
				return Stacks{}, &UnknownStackError{Flag: l.flag, Name: n, Discovered: discovered}
			}
			if prev, dup := placed[n]; dup {
				if prev == l.flag {
					continue
				}
				return Stacks{}, &UnknownStackError{Flag: l.flag, Name: n + " (already in " + prev + ")", Discovered: discovered}
			}
			placed[n] = l.flag
			*l.into = append(*l.into, n)
		}
	}
	var unsorted []string
	for _, s := range discovered {
		if _, ok := placed[s]; !ok {
			unsorted = append(unsorted, s)
		}
	}
	if len(unsorted) > 0 {
		return Stacks{}, &UnsortedError{Names: unsorted}
	}
	return out, nil
}

// PromptPath is where init copies the shipped prompt, and what the config
// it writes points prompts.implement at: README step 7's "copy the file
// into your repository, replace that block with what is true of yours, and
// point prompts.implement at the copy".
const PromptPath = "prompts/implement.md"

// ConfigJSON is .github/falconet.json as init writes it: the two stack
// lists and the one prompt override, indented two spaces, one trailing
// newline, nothing else — every other key has a default that transfers
// (README step 7), and a file that repeats the defaults is a file that
// drifts from them.
func ConfigJSON(stacks Stacks) []byte {
	var c struct {
		Stacks struct {
			Plan         []string `json:"plan"`
			ValidateOnly []string `json:"validate_only"`
		} `json:"stacks"`
		Prompts struct {
			Implement string `json:"implement"`
		} `json:"prompts"`
	}
	// Empty lists, not null: `"plan": null` is not what the schema reads.
	c.Stacks.Plan = append([]string{}, stacks.Plan...)
	c.Stacks.ValidateOnly = append([]string{}, stacks.ValidateOnly...)
	c.Prompts.Implement = PromptPath
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		// A struct of strings cannot fail to marshal; this is a build defect.
		panic(err)
	}
	return append(out, '\n')
}

// --- step 8: the caller workflow --------------------------------------------

// workflowTemplate is README step 8's file, post-cutover (#19): the
// reusable workflow at this binary's own version, no falconet-ref input
// (the binary is installed by the workflow from that same version — one
// coordinate, ADR-0006 D6), the four secrets, and the two blocks whose
// comments the README carries because each one was an incident: two runs
// racing to open two pull requests, and a startup_failure that told nobody.
const workflowTemplate = `name: infra requests

on:
  issues:
    types: [opened, labeled, reopened]
  issue_comment:
    types: [created]

# One run per issue. ` + "`opened`" + ` and ` + "`labeled`" + ` arrive seconds apart on a freshly
# filed request, and without this they are two runs racing to open two pull
# requests for the same issue.
concurrency:
  group: falconet-${{ github.event.issue.number }}
  cancel-in-progress: false

# A called workflow can only narrow the caller's token, never widen it, so
# each of these must be at least what the widest job inside declares —
# ` + "`publish`" + ` declares ` + "`contents: write`" + ` to push. That check happens when the
# file is LOADED: grant less and the run is a ` + "`startup_failure`" + ` with no jobs,
# no logs and nothing on the issue.
#
# It is narrower than it reads. ` + "`implement`" + `, the job that runs the agent,
# declares ` + "`permissions: {}`" + ` and holds no token at all; ` + "`gate`" + ` and ` + "`contain`" + `
# narrow themselves back to ` + "`contents: read`" + `. Only ` + "`publish`" + ` receives this,
# and it pushes with the App token in any case.
permissions:
  contents: write
  issues: write
  pull-requests: write

jobs:
  falconet:
    uses: ` + doctor.Reusable + `@<VERSION>
    with:
      issue: ${{ github.event.issue.number }}
    secrets:
      app-id: ${{ secrets.FALCONET_APP_ID }}
      app-private-key: ${{ secrets.FALCONET_APP_PRIVATE_KEY }}
      anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
      plan-env: ${{ secrets.FALCONET_PLAN_ENV }}
`

// Workflow is the caller workflow pinned to ref.
func Workflow(ref string) []byte {
	return []byte(strings.Replace(workflowTemplate, "@<VERSION>", "@"+ref, 1))
}

// Uses is the `uses:` value the workflow carries at ref, for the report.
func Uses(ref string) string {
	return doctor.Reusable + "@" + ref
}

// pseudoVersion is what the go command records for a module built from a
// commit with no tag: vX.Y.Z-[pre.]0.YYYYMMDDHHMMSS-abcdefabcdef.
// Build metadata after it — `+dirty`, which the go command appends when the
// tree had uncommitted changes, or `+incompatible` — is still a
// pseudo-version; no uses: line can fetch those either.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}(\+[0-9A-Za-z.-]+)?$`)

// WorkflowRef is the ref the `uses:` line pins, from this binary's version:
// a release tag pins itself — one coordinate, the binary and the workflow
// that installs it (ADR-0006 D6). A binary with no tag to name — a `dev`
// build, or a `go install` of a commit, whose module version is a
// pseudo-version that no `uses:` line could fetch — pins `main`, which is
// where the README's template starts and what doctor notes as "unpinned —
// pin a SHA or tag once a canary has reached a pull request".
func WorkflowRef(version string) string {
	switch {
	case version == "", version == "dev", version == "(devel)":
		return "main"
	case pseudoVersion.MatchString(version):
		return "main"
	}
	return version
}

// --- step 2: the .gitignore line -------------------------------------------

// AppendIgnore adds entry as a line of content unless a line already says
// exactly that, and reports whether anything changed. A file that does not
// end in a newline gets one first, so the entry is its own line. Applying
// it twice is applying it once.
func AppendIgnore(content []byte, entry string) (out []byte, changed bool) {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == entry {
			return content, false
		}
	}
	out = append([]byte{}, content...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, entry...)
	out = append(out, '\n')
	return out, true
}

// IgnoreEntry is the .gitignore line for a handoff directory: the name with
// one trailing slash, as README step 2 writes it, so it matches a directory
// and nothing else.
func IgnoreEntry(handoffDir string) string {
	return strings.TrimSuffix(handoffDir, "/") + "/"
}

// --- the commit --------------------------------------------------------------

// Written is one file init wrote, and what it is for, for the commit body.
type Written struct {
	Path, What string
}

// CommitSubject is the one line every init commit begins with.
const CommitSubject = "Install falconet"

// CommitMessage is the message file `git commit -F` reads: the subject,
// then each file written and what it is, so the commit says what was added
// the way a person's would.
func CommitMessage(written []Written) []byte {
	var b strings.Builder
	b.WriteString(CommitSubject + "\n\nWritten by `falconet init` (README steps 2, 7 and 8):\n\n")
	width := 0
	for _, w := range written {
		if len(w.Path) > width {
			width = len(w.Path)
		}
	}
	for _, w := range written {
		fmt.Fprintf(&b, "  %-*s   %s\n", width, w.Path, w.What)
	}
	return []byte(b.String())
}

// --- the report --------------------------------------------------------------

// Summary is init's last line before "Left for you:", counting every status
// the run printed. Notes are not counted, as doctor does not count them.
func Summary(r doctor.Report) string {
	return fmt.Sprintf("init: %d ok, %d done, %d skipped, %d missing, %d cannot tell",
		r.Count(doctor.OK), r.Count(doctor.Done), r.Count(doctor.Skipped), r.Count(doctor.Missing), r.Count(doctor.CannotTell))
}

// LeftForYou is the closing block: what init did not do, in order, each in
// the README's words. Empty is still a block with the canary in it, since
// the canary is always left — but the caller builds the list, so here an
// empty list prints nothing.
func LeftForYou(items []string) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nLeft for you:\n")
	for i, item := range items {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, item)
	}
	return b.String()
}

// The items, in the README's words. Each names its step so the reader can
// find it in the README.
const (
	LeftApp = "step 3 — the GitHub App: falconet init with FALCONET_SETUP_TOKEN set registers one by manifest from your browser and stores its two secrets; " +
		"or by hand (Settings → Developer settings → GitHub Apps → New GitHub App; " +
		"webhook off; repository permissions Contents, Issues and Pull requests: read and write; installable only on this account), " +
		"note its App ID, generate a private key, Install App on this repository, then: " +
		"falconet init --app-id <App ID> --app-key <the .pem>"
	LeftAnthropic = "step 4 — store the Anthropic API key (an API key from the console, not a Claude Code subscription token): " +
		"falconet init with FALCONET_SETUP_TOKEN set reads it from a no-echo prompt, or from stdin   (or: gh secret set ANTHROPIC_API_KEY)"
	LeftPlanEnv = "step 5 — store the planning environment, one JSON object of the variables the planned stacks need " +
		"(backend keys, provider tokens, TF_VAR_*; read-only credentials; contents, not paths), written to a file OUTSIDE the repository: " +
		"falconet init --plan-env-file <that file>, then delete it   (or: gh secret set FALCONET_PLAN_ENV < that-file)"
	LeftLabels = "step 6 — create the four labels: falconet init with FALCONET_SETUP_TOKEN set   " +
		"(or: for l in infra-request needs-info ready-for-human needs-plan-review; do gh label create \"$l\"; done)"
	LeftPrompt = "step 7 — edit the standing-facts block in " + PromptPath + ": it describes the repository falconet was extracted from " +
		"(its registrar sandbox, its scratch tenant), and the agent will believe it of this one until it says what is true here"
	LeftPromptUnset = "step 7 — prompts.implement is not set, so the shipped prompt is used, and its standing facts are the origin's: " +
		"copy it into the repository (falconet prompt implement > " + PromptPath + "), edit that block, and point prompts.implement at the copy"
	LeftCanary = "step 9 — file a canary issue: the smallest change the planned stack can carry (one DNS record, one tag), " +
		"labelled infra-request, then watch the run; once it has reached a pull request, pin the ref in uses: to the SHA or tag you ran"
	LeftDoctor = "then: falconet doctor"
)

// LeftInstall is the App registered and its secrets stored, with the
// installation not made within the wait: the click, and the check after it.
func LeftInstall(installURL, repository string) string {
	return "step 3 — install the App: " + installURL + " → Install → Only select repositories → " + repository + ", then: falconet doctor"
}

// LeftPush is the push init never runs.
func LeftPush(branch string) string {
	if branch == "" {
		return "git push origin HEAD   (the checkout is detached; name the branch to push)"
	}
	return "git push origin " + branch
}

// LeftCommit is the push after a --no-commit run: the files are staged and
// the commit is the operator's.
func LeftCommit(branch string) string {
	return "git commit   (the files are staged; --no-commit left the commit to you), then " + LeftPush(branch)
}

// LeftFix is what doctor found wrong and init could not put right: the
// step, the finding, and doctor's own hint.
func LeftFix(l doctor.Line) string {
	s := fmt.Sprintf("step %d — %s", l.Step, l.Text)
	if l.Hint != "" {
		s += ": " + l.Hint
	}
	return s
}
