package doctor

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/zetlen/falconet/internal/github"
)

const maxCount = 5000

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// --- the report's shape --------------------------------------------------------

func TestLineString(t *testing.T) {
	for _, tc := range []struct {
		line Line
		want string
	}{
		{Line{OK, 1, "the repository has issues enabled", ""},
			"ok           1. the repository has issues enabled"},
		{Line{Missing, 5, "label needs-info", "create it: gh label create needs-info   (or: falconet init)"},
			"MISSING      5. label needs-info\n             create it: gh label create needs-info   (or: falconet init)"},
		{Line{CannotTell, 3, "secret FALCONET_APP_ID (no FALCONET_SETUP_TOKEN)", ""},
			"cannot tell  3. secret FALCONET_APP_ID (no FALCONET_SETUP_TOKEN)"},
		{Line{Note, 1, "default_workflow_permissions is read (fine: the caller workflow grants what it needs)", ""},
			"note         1. default_workflow_permissions is read (fine: the caller workflow grants what it needs)"},
		{Line{Note, 0, "the token is classic", ""},
			"note         the token is classic"},
	} {
		if got := tc.line.String(); got != tc.want {
			t.Errorf("\n got %q\nwant %q", got, tc.want)
		}
	}
}

// Whatever the status, step and text, the text starts at Column and so does
// the hint: the column is the contract the reader's eye relies on.
func TestTheColumnNeverMoves(t *testing.T) {
	check(t, func(status uint8, step uint8, text, hint string) bool {
		l := Line{Status(status % 4), int(step % 9), strings.ReplaceAll(text, "\n", " "), strings.ReplaceAll(hint, "\n", " ")}
		out := l.String()
		lines := strings.Split(out, "\n")
		if len(lines) != 1+btoi(l.Hint != "") {
			return false
		}
		first := lines[0]
		if len(first) < Column || !strings.HasPrefix(first, l.Status.String()) || strings.TrimRight(first[:Column], " ") != l.Status.String() {
			return false
		}
		rest := first[Column:]
		if l.Step > 0 {
			if !strings.HasPrefix(rest, fmt.Sprintf("%d. ", l.Step)) {
				return false
			}
			rest = rest[3:]
		}
		if rest != l.Text {
			return false
		}
		if l.Hint != "" && lines[1] != strings.Repeat(" ", Column)+l.Hint {
			return false
		}
		return true
	})
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestReportCountsNotesNowhere(t *testing.T) {
	r := Report{
		{OK, 1, "a", ""}, {OK, 1, "b", ""}, {Note, 1, "n", ""},
		{Missing, 6, "m", ""}, {CannotTell, 3, "c", ""}, {CannotTell, 4, "d", ""},
	}
	if got := r.Summary(); got != "doctor: 2 ok, 1 missing, 2 cannot tell" {
		t.Errorf("summary: %q", got)
	}
	if r.Clean() {
		t.Error("a report with a MISSING is not clean")
	}
	if !(Report{{OK, 1, "a", ""}, {Note, 8, "n", ""}}).Clean() {
		t.Error("notes do not spoil a clean report")
	}
	if (Report{{OK, 1, "a", ""}, {CannotTell, 1, "c", ""}}).Clean() {
		t.Error("cannot tell is not ok")
	}
	if !(Report{}).Clean() {
		t.Error("an empty report is clean")
	}
}

// --- refusals, and the token -----------------------------------------------------

func TestRefusedNamesThePermissionOnlyWhenGitHubSaidNo(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&github.Error{Method: "GET", Path: "/x", Status: 403, Message: "Resource not accessible by personal access token"},
			"cannot tell  3. secret X (403 Resource not accessible by personal access token — needs Secrets: read)"},
		{&github.Error{Method: "GET", Path: "/x", Status: 404, Message: "Not Found"},
			"cannot tell  3. secret X (404 not found, or no access — needs Secrets: read)"},
		{&github.Error{Method: "GET", Path: "/x", Status: 500, Message: "boom"},
			"cannot tell  3. secret X (500 boom)"},
		{&github.Error{Method: "GET", Path: "/x", Status: 502},
			"cannot tell  3. secret X (502 Bad Gateway)"},
		{errors.New("dial tcp 127.0.0.1:1: connect: connection refused"),
			"cannot tell  3. secret X (GITHUB_API_URL unreachable)"},
	} {
		if got := Refused(3, "secret X", tc.err, NeedsSecrets).String(); got != tc.want {
			t.Errorf("%v:\n got %q\nwant %q", tc.err, got, tc.want)
		}
	}
	if got := NoToken(5, "label x").String(); got != "cannot tell  5. label x (no FALCONET_SETUP_TOKEN)" {
		t.Errorf("NoToken: %q", got)
	}
}

func TestClassicTokenNotesMissingRepoOnce(t *testing.T) {
	for _, tc := range []struct {
		scopes string
		note   bool
		want   string
	}{
		{"", false, ""},
		{"repo", false, ""},
		{"repo, gist", false, ""},
		{"gist, repo", false, ""},
		{" gist ", true, "note         the token is classic and its scopes (gist) do not include repo, which a classic token needs"},
		{"public_repo, workflow", true, "note         the token is classic and its scopes (public_repo, workflow) do not include repo, which a classic token needs"},
		{"repo:status", true, "note         the token is classic and its scopes (repo:status) do not include repo, which a classic token needs"},
	} {
		line, note := ClassicToken(tc.scopes)
		if note != tc.note || (note && line.String() != tc.want) {
			t.Errorf("%q: got (%q, %v), want (%q, %v)", tc.scopes, line.String(), note, tc.want, tc.note)
		}
	}
}

func TestTheTokenHintCarriesTheTableAndTheAdvice(t *testing.T) {
	for _, want := range []string{"FALCONET_SETUP_TOKEN", "seven-day", "Administration", "Actions", "Secrets", "Issues",
		"read", "write", "classic token needs the repo scope", "GITHUB_TOKEN and GH_TOKEN are deliberately"} {
		if !strings.Contains(TokenHint, want) {
			t.Errorf("TokenHint lacks %q", want)
		}
	}
}

// --- step 1 ----------------------------------------------------------------------

func TestIssues(t *testing.T) {
	if got := Issues(&github.Repository{HasIssues: true}).String(); got != "ok           1. the repository has issues enabled" {
		t.Errorf("%q", got)
	}
	got := Issues(&github.Repository{HasIssues: false})
	if got.Status != Missing || got.Text != "the repository has issues disabled" || got.Hint == "" {
		t.Errorf("%+v", got)
	}
}

func sel(owned, verified bool, patterns ...string) *github.SelectedActions {
	return &github.SelectedActions{GithubOwnedAllowed: owned, VerifiedAllowed: verified, PatternsAllowed: patterns}
}

func TestActionsPolicy(t *testing.T) {
	all := &github.ActionsPermissions{Enabled: true, AllowedActions: "all"}
	selected := &github.ActionsPermissions{Enabled: true, AllowedActions: "selected"}
	for _, tc := range []struct {
		name string
		p    *github.ActionsPermissions
		sel  *github.SelectedActions
		want string
	}{
		{"all", all, nil, "ok           1. allowed_actions is all"},
		{"disabled", &github.ActionsPermissions{Enabled: false, AllowedActions: "all"}, nil,
			"MISSING      1. Actions are disabled for this repository"},
		{"local_only", &github.ActionsPermissions{Enabled: true, AllowedActions: "local_only"}, nil,
			"MISSING      1. allowed_actions is local_only: workflows from outside the repository cannot run"},
		{"selected, every one covered", selected,
			sel(true, false, "zetlen/falconet", "anthropics/claude-code-action@*"),
			"ok           1. allowed_actions is selected, covering zetlen/falconet, actions/* and anthropics/claude-code-action"},
		{"selected, covered by wide patterns", selected,
			sel(false, false, "zetlen/*", "actions/*", "anthropics/*"),
			"ok           1. allowed_actions is selected, covering zetlen/falconet, actions/* and anthropics/claude-code-action"},
		{"selected, github-owned covered by github_owned_allowed and nothing else", selected,
			sel(true, false),
			"MISSING      1. allowed_actions is selected and does not cover zetlen/falconet, anthropics/claude-code-action"},
		{"selected, nothing at all", selected,
			sel(false, false),
			"MISSING      1. allowed_actions is selected and does not cover zetlen/falconet, actions/*, anthropics/claude-code-action"},
		{"selected, one short", selected,
			sel(true, false, "zetlen/falconet", "opentofu/setup-opentofu"),
			"MISSING      1. allowed_actions is selected and does not cover anthropics/claude-code-action"},
		{"selected, github-owned by patterns for each", selected,
			sel(false, false, "zetlen/falconet", "anthropics/claude-code-action",
				"actions/checkout@*", "actions/upload-artifact@*", "actions/download-artifact@*", "actions/create-github-app-token@*"),
			"ok           1. allowed_actions is selected, covering zetlen/falconet, actions/* and anthropics/claude-code-action"},
		{"selected, github-owned by patterns for some", selected,
			sel(false, false, "zetlen/falconet", "anthropics/claude-code-action", "actions/checkout@*"),
			"MISSING      1. allowed_actions is selected and does not cover actions/*"},
		{"selected, no list read", selected, nil,
			"cannot tell  1. allowed_actions is selected (the selected-actions list was not read)"},
		{"a value doctor does not know", &github.ActionsPermissions{Enabled: true, AllowedActions: "some_new_policy"}, nil,
			"cannot tell  1. allowed_actions is some_new_policy (a value doctor does not know)"},
	} {
		got := ActionsPolicy(tc.p, tc.sel)
		if first := strings.SplitN(got.String(), "\n", 2)[0]; first != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, first, tc.want)
		}
		if got.Status == Missing && got.Hint == "" {
			t.Errorf("%s: a MISSING without a hint", tc.name)
		}
	}
	verified := ActionsPolicy(selected, sel(true, true, "zetlen/falconet"))
	if !strings.Contains(verified.Hint, "verified_allowed is on") {
		t.Errorf("verified_allowed is mentioned when it might be what covers a creator: %q", verified.Hint)
	}
	if plain := ActionsPolicy(selected, sel(true, false, "zetlen/falconet")); strings.Contains(plain.Hint, "verified_allowed") {
		t.Errorf("and not otherwise: %q", plain.Hint)
	}
}

func TestCovers(t *testing.T) {
	falconet := Action{"zetlen", "falconet", ".github/workflows/falconet.yml"}
	claude := Action{"anthropics", "claude-code-action", ""}
	for _, tc := range []struct {
		pattern string
		a       Action
		want    bool
	}{
		// The README's own list.
		{"zetlen/falconet", falconet, true},
		{"anthropics/claude-code-action", claude, true},
		// GitHub's documented forms.
		{"anthropics/claude-code-action@*", claude, true},
		{"anthropics/claude-code-action@v1", claude, true},
		{"anthropics/claude-code-action@a4c9", claude, true},
		{"anthropics/*", claude, true},
		{"anthropics*/*", claude, true},
		{"*/claude-code-action@*", claude, true},
		{"*/claude**@*", claude, true},
		{"*", claude, true},
		{"*/*", claude, true},
		{"zetlen/falconet/.github/workflows/falconet.yml@*", falconet, true},
		{"zetlen/falconet/.github/workflows/*", falconet, true},
		{"zetlen/falconet/*", falconet, true},
		{"ZETLEN/Falconet", falconet, true},
		{" zetlen/falconet ", falconet, true},
		// Not this one.
		{"anthropics/claude-code", claude, false},
		{"anthropics/claude-code-action-x", claude, false},
		{"anthropic/*", claude, false},
		{"zetlen/falconet", claude, false},
		{"zetlen/falconet/.github/workflows/other.yml@*", falconet, false},
		{"*.yml", claude, false},
		{"", claude, false},
		{"@*", claude, false},
		// A literal that is regexp syntax stays literal.
		{"anthropics/claude.code-action", claude, false},
		{"anthropics/(claude-code-action)", claude, false},
	} {
		if got := Covers(tc.pattern, tc.a); got != tc.want {
			t.Errorf("Covers(%q, %s) = %v, want %v", tc.pattern, tc.a, got, tc.want)
		}
	}
}

// name is a random owner or repository name from GitHub's alphabet.
type name string

func (name) Generate(r *rand.Rand, size int) reflect.Value {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_."
	n := 1 + r.Intn(12)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return reflect.ValueOf(name(b))
}

// For any owner/repo: the entry naming it covers it in every documented
// spelling, `*` in the owner or the repo covers it, and a different owner or
// a longer repo name does not.
func TestCoversIsExactWithoutAStarAndWideWithOne(t *testing.T) {
	check(t, func(owner, repo, other name) bool {
		a := Action{string(owner), string(repo), ""}
		exact := string(owner) + "/" + string(repo)
		for _, p := range []string{exact, exact + "@*", exact + "@v1", string(owner) + "/*", "*/" + string(repo), "*", "*/*", exact + "*"} {
			if !Covers(p, a) {
				return false
			}
		}
		if Covers(exact+"x", a) || Covers("x"+exact, a) {
			return false
		}
		if !strings.EqualFold(string(other), string(owner)) && Covers(string(other)+"/"+string(repo), a) {
			return false
		}
		return true
	})
}

func TestWorkflowPermissionsNoteAndRunnersNote(t *testing.T) {
	if got := WorkflowPermissionsNote(&github.WorkflowPermissions{DefaultWorkflowPermissions: "read"}).String(); got !=
		"note         1. default_workflow_permissions is read (fine: the caller workflow grants what it needs)" {
		t.Errorf("%q", got)
	}
	if got := WorkflowPermissionsNote(&github.WorkflowPermissions{DefaultWorkflowPermissions: "write"}); got.Status != Note || !strings.Contains(got.Text, "is write") {
		t.Errorf("%+v", got)
	}
	if got := RunnersNote(); got.Status != Note || got.Step != 1 || !strings.Contains(got.Text, "Linux x64") {
		t.Errorf("%+v", got)
	}
}

// --- step 2 ----------------------------------------------------------------------

func TestHandoffIgnored(t *testing.T) {
	if got := HandoffIgnored(".falconet", true).String(); got != "ok           2. .falconet/ is gitignored" {
		t.Errorf("%q", got)
	}
	if got := HandoffIgnored(".falconet/", true).String(); got != "ok           2. .falconet/ is gitignored" {
		t.Errorf("a trailing slash is not doubled: %q", got)
	}
	got := HandoffIgnored("scratch", false)
	if got.String() != "MISSING      2. scratch/ is not gitignored\n             printf 'scratch/\\n' >> .gitignore   (or: falconet init)" {
		t.Errorf("%q", got.String())
	}
}

// --- steps 3–4 -------------------------------------------------------------------

func TestSecretLines(t *testing.T) {
	all := []string{"FALCONET_APP_ID", "FALCONET_APP_PRIVATE_KEY", "ANTHROPIC_API_KEY", "SOMETHING_ELSE"}
	got := strs(SecretLines(all))
	want := []string{
		"ok           3. secret FALCONET_APP_ID exists (a value can never be read back, so the name is the check)",
		"ok           3. secret FALCONET_APP_PRIVATE_KEY exists (a value can never be read back, so the name is the check)",
		"ok           4. secret ANTHROPIC_API_KEY exists (a value can never be read back, so the name is the check)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	got = strs(SecretLines(nil))
	want = []string{
		"MISSING      3. secret FALCONET_APP_ID\n             store it: gh secret set FALCONET_APP_ID   (or: falconet init)",
		"MISSING      3. secret FALCONET_APP_PRIVATE_KEY\n             store it: gh secret set FALCONET_APP_PRIVATE_KEY   (or: falconet init)",
		"MISSING      4. secret ANTHROPIC_API_KEY\n             store it: gh secret set ANTHROPIC_API_KEY   (or: falconet init)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// --- step 5 ----------------------------------------------------------------------

func TestLabelLines(t *testing.T) {
	labels := Labels{"infra-request", "needs-info", "ready-for-human", "needs-plan-review"}
	got := strs(LabelLines(labels, []string{"infra-request", "ready-for-human", "needs-plan-review", "bug"}))
	want := []string{
		"ok           5. label infra-request",
		"MISSING      5. label needs-info\n             create it: gh label create needs-info   (or: falconet init)",
		"ok           5. label ready-for-human",
		"ok           5. label needs-plan-review",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	// Configured names, not the defaults; and two keys on one label is two lines.
	got = strs(LabelLines(Labels{"queue", "same", "same", "pr"}, []string{"same"}))
	if len(got) != 4 || got[1] != "ok           5. label same" || got[2] != got[1] || !strings.HasPrefix(got[0], "MISSING      5. label queue") {
		t.Errorf("%q", got)
	}
}

// --- step 6 ----------------------------------------------------------------------

func TestConfigLine(t *testing.T) {
	if got := ConfigLine(".github/falconet.json", nil).String(); got != "ok           6. .github/falconet.json parses" {
		t.Errorf("%q", got)
	}
	if got := ConfigLine("", nil); got.Status != OK || !strings.Contains(got.Text, "defaults stand alone") {
		t.Errorf("%+v", got)
	}
	err := errors.New(".github/falconet.json is not valid JSON: invalid character '}' looking for beginning of value")
	got := ConfigLine(".github/falconet.json", err)
	if got.String() != "MISSING      6. the config does not parse: \".github/falconet.json is not valid JSON: invalid character '}' looking for beginning of value\"\n             check it: jq -e . .github/falconet.json" {
		t.Errorf("%q", got.String())
	}
	if got := ConfigLine("", errors.New("x")); !strings.Contains(got.Hint, ".github/falconet.json") {
		t.Errorf("the hint has a file to name even when none was read: %q", got.Hint)
	}
}

func TestPromptLines(t *testing.T) {
	got := strs(PromptLines([]Prompt{
		{"implement", "prompts/implement.md", true, true},
		{"pause_needs_info", "prompts/ask.md", true, false},
		{"park_needs_info", "prompts/park-needs-info.md", true, true},
		{"implement", "/etc/passwd", false, true},
		{"implement", "../../prompts/implement.md", false, false},
	}))
	want := []string{
		"ok           6. prompts.implement names prompts/implement.md, which exists",
		"MISSING      6. prompts.pause_needs_info names prompts/ask.md, which does not exist\n             copy the shipped prompt into the repository and point the key at the copy (README step 6)",
		"note         6. prompts.park_needs_info is not a prompt falconet reads (the two are implement and pause_needs_info)",
		"MISSING      6. prompts.implement names /etc/passwd, which is not under the repository root\n             a prompt path is relative to the repository root (README step 6)",
		"MISSING      6. prompts.implement names ../../prompts/implement.md, which is not under the repository root\n             a prompt path is relative to the repository root (README step 6)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if got := PromptLines(nil); len(got) != 0 {
		t.Errorf("no overrides, no lines: %q", strs(got))
	}
}

// --- step 7 ----------------------------------------------------------------------

const canonicalCaller = `name: infra requests

on:
  issues:
    types: [opened, labeled, reopened]
  issue_comment:
    types: [created]

concurrency:
  group: falconet-${{ github.event.issue.number }}
  cancel-in-progress: false

# A called workflow can only narrow the caller's token, never widen it.
permissions:
  contents: write
  issues: write
  pull-requests: write

jobs:
  falconet:
    uses: zetlen/falconet/.github/workflows/falconet.yml@a3ed1b3fcb49f4bf91792f3191790e95bd47a102
    with:
      issue: ${{ github.event.issue.number }}
    secrets:
      app-id: ${{ secrets.FALCONET_APP_ID }}
`

func TestParseCaller(t *testing.T) {
	c := ParseCaller([]byte(canonicalCaller))
	want := Caller{HasUses: true, Ref: "a3ed1b3fcb49f4bf91792f3191790e95bd47a102", HasPermissions: true,
		Grants: []Permission{{"contents", "write"}, {"issues", "write"}, {"pull-requests", "write"}}}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("\n got %+v\nwant %+v", c, want)
	}
	for _, tc := range []struct {
		name, text string
		want       Caller
	}{
		{"empty", "", Caller{}},
		{"write-all", "permissions: write-all\njobs:\n  x:\n    uses: zetlen/falconet/.github/workflows/falconet.yml@main\n",
			Caller{HasUses: true, Ref: "main", HasPermissions: true, Inline: "write-all"}},
		{"read-all", "permissions: read-all\n", Caller{HasPermissions: true, Inline: "read-all"}},
		{"an empty mapping", "permissions: {}\n", Caller{HasPermissions: true}},
		{"a flow mapping", "permissions: { contents: write, issues: read }\n",
			Caller{HasPermissions: true, Grants: []Permission{{"contents", "write"}, {"issues", "read"}}}},
		{"quoted values and trailing comments",
			"permissions:\n  contents: \"write\" # to push\n  issues: 'write'\n  pull-requests: write   # comment\n",
			Caller{HasPermissions: true, Grants: []Permission{{"contents", "write"}, {"issues", "write"}, {"pull-requests", "write"}}}},
		{"a comment between entries ends nothing",
			"permissions:\n  contents: write\n  # why\n\n  issues: write\njobs:\n",
			Caller{HasPermissions: true, Grants: []Permission{{"contents", "write"}, {"issues", "write"}}}},
		{"a job-level block is not the top-level one",
			"jobs:\n  x:\n    permissions:\n      contents: read\n    uses: zetlen/falconet/.github/workflows/falconet.yml@v1\n",
			Caller{HasUses: true, Ref: "v1"}},
		{"the block ends at the next top-level key",
			"permissions:\n  contents: write\njobs:\n  x:\n    issues: write\n",
			Caller{HasPermissions: true, Grants: []Permission{{"contents", "write"}}}},
		{"a uses: line naming something else is not it",
			"jobs:\n  x:\n    uses: someone/else/.github/workflows/falconet.yml@v1\n", Caller{}},
		{"a uses: with no ref", "jobs:\n  x:\n    uses: zetlen/falconet/.github/workflows/falconet.yml\n", Caller{}},
		{"a quoted uses:", "jobs:\n  x:\n    uses: \"zetlen/falconet/.github/workflows/falconet.yml@v1\"\n",
			Caller{HasUses: true, Ref: "v1"}},
		{"a step-shaped uses:", "steps:\n  - uses: zetlen/falconet/.github/workflows/falconet.yml@v1\n",
			Caller{HasUses: true, Ref: "v1"}},
		{"CRLF line ends", "permissions:\r\n  contents: write\r\n",
			Caller{HasPermissions: true, Grants: []Permission{{"contents", "write"}}}},
		{"tabs would not be YAML, but do not confuse the parser",
			"permissions:\n\tcontents: write\n", Caller{HasPermissions: true, Grants: []Permission{{"contents", "write"}}}},
	} {
		if got := ParseCaller([]byte(tc.text)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

// The README's step 7 is the specification, and contract.test.sh holds it to
// falconet.yml; this holds RequiredPermissions and Reusable to it, so the
// three cannot drift apart without one of the two tests saying so.
func TestTheREADMEsCallerIsWhatDoctorAsksFor(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Skip("no README beside the package")
	}
	text := string(raw)
	start := strings.Index(text, "### 7.")
	end := strings.Index(text, "### 8.")
	if start < 0 || end < 0 {
		t.Fatal("README step 7 not found")
	}
	step8 := text[start:end]
	open := strings.Index(step8, "```yaml\n")
	if open < 0 {
		t.Fatal("no yaml block in step 7")
	}
	block := step8[open+len("```yaml\n"):]
	block = block[:strings.Index(block, "```")]
	c := ParseCaller([]byte(block))
	if !c.HasUses || c.Ref != "main" {
		t.Errorf("the README's caller uses %s@main; parsed %+v", Reusable, c)
	}
	if !reflect.DeepEqual(c.Grants, RequiredPermissions) {
		t.Errorf("README step 7 grants %v, doctor asks for %v", c.Grants, RequiredPermissions)
	}
	lines := WorkflowLines([]byte(block), true)
	for _, l := range lines {
		if l.Status == Missing {
			t.Errorf("the README's own caller is MISSING something: %s", l)
		}
	}
}

func TestWorkflowLines(t *testing.T) {
	const usesLine = "ok           7. it uses zetlen/falconet/.github/workflows/falconet.yml@a3ed1b3fcb49f4bf91792f3191790e95bd47a102"
	const grants = "ok           7. permissions grants contents: write, issues: write, pull-requests: write"
	const exists = "ok           7. .github/workflows/infra-requests.yml exists"
	startup := "\n             the run would be a startup_failure: no jobs, no logs, nothing on the issue. Grant README step 7's permissions: block, verbatim"
	sub := func(old, new string) string { return strings.Replace(canonicalCaller, old, new, 1) }
	for _, tc := range []struct {
		name   string
		text   string
		exists bool
		want   []string
	}{
		{"no file", "", false, []string{
			"MISSING      7. .github/workflows/infra-requests.yml\n             write it from README step 7   (or: falconet init)"}},
		{"the canonical caller", canonicalCaller, true, []string{exists, usesLine, grants}},
		{"contents: read — the startup_failure the README opens with",
			sub("contents: write", "contents: read"), true, []string{exists, usesLine,
				"MISSING      7. permissions grants contents: read, and falconet's widest job declares contents: write, issues: write, pull-requests: write" + startup}},
		{"two short, one absent",
			sub("  contents: write\n  issues: write\n", "  contents: read\n"), true, []string{exists, usesLine,
				"MISSING      7. permissions grants contents: read, issues: none, and falconet's widest job declares contents: write, issues: write, pull-requests: write" + startup}},
		{"no block at all",
			sub("permissions:\n  contents: write\n  issues: write\n  pull-requests: write\n", ""), true, []string{exists, usesLine,
				"MISSING      7. no top-level permissions: block, and falconet's widest job declares contents: write, issues: write, pull-requests: write" + startup}},
		{"an empty mapping",
			sub("permissions:\n  contents: write\n  issues: write\n  pull-requests: write\n", "permissions: {}\n"), true, []string{exists, usesLine,
				"MISSING      7. permissions grants contents: none, issues: none, pull-requests: none, and falconet's widest job declares contents: write, issues: write, pull-requests: write" + startup}},
		{"read-all",
			sub("permissions:\n  contents: write\n  issues: write\n  pull-requests: write\n", "permissions: read-all\n"), true, []string{exists, usesLine,
				"MISSING      7. permissions: read-all grants none of contents: write, issues: write, pull-requests: write" + startup}},
		{"write-all is ok, and more than needed",
			sub("permissions:\n  contents: write\n  issues: write\n  pull-requests: write\n", "permissions: write-all\n"), true, []string{exists, usesLine,
				"ok           7. permissions: write-all grants contents: write, issues: write, pull-requests: write",
				"note         7. write-all grants every permission, which is more than falconet needs; step 7's three lines are enough"}},
		{"granting more is a note",
			sub("  pull-requests: write\n", "  pull-requests: write\n  actions: read\n  id-token: write\n"), true, []string{exists, usesLine, grants,
				"note         7. permissions also grants actions: read, id-token: write, which nothing in falconet needs"}},
		{"@main is a note, not MISSING",
			strings.ReplaceAll(canonicalCaller, "a3ed1b3fcb49f4bf91792f3191790e95bd47a102", "main"), true, []string{exists,
				"ok           7. it uses zetlen/falconet/.github/workflows/falconet.yml@main",
				"note         7. the ref is main: unpinned — pin a SHA or tag once a canary has reached a pull request", grants}},
		// falconet-ref is no input since #19, and a reusable workflow rejects
		// an input it does not declare at load: MISSING, whatever the value.
		{"falconet-ref still passed, in step with uses:",
			sub("      issue: ${{ github.event.issue.number }}\n", "      issue: ${{ github.event.issue.number }}\n      falconet-ref: a3ed1b3fcb49f4bf91792f3191790e95bd47a102\n"), true, []string{exists, usesLine,
				"MISSING      7. falconet-ref is no longer an input; remove it\n             the run would be a startup_failure: a reusable workflow rejects an input it does not declare when the caller's file is loaded", grants}},
		{"falconet-ref still passed, as main",
			sub("      issue: ${{ github.event.issue.number }}\n", "      issue: ${{ github.event.issue.number }}\n      falconet-ref: main\n"), true, []string{exists, usesLine,
				"MISSING      7. falconet-ref is no longer an input; remove it\n             the run would be a startup_failure: a reusable workflow rejects an input it does not declare when the caller's file is loaded", grants}},
		{"falconet-ref still passed, empty",
			sub("      issue: ${{ github.event.issue.number }}\n", "      issue: ${{ github.event.issue.number }}\n      falconet-ref:\n"), true, []string{exists, usesLine,
				"MISSING      7. falconet-ref is no longer an input; remove it\n             the run would be a startup_failure: a reusable workflow rejects an input it does not declare when the caller's file is loaded", grants}},
		{"no uses: line",
			sub("uses: zetlen/falconet/.github/workflows/falconet.yml@a3ed1b3fcb49f4bf91792f3191790e95bd47a102", "uses: someone/else@v1"), true, []string{exists,
				"MISSING      7. no uses: line names zetlen/falconet/.github/workflows/falconet.yml\n             jobs.falconet.uses: zetlen/falconet/.github/workflows/falconet.yml@<sha or tag>", grants}},
	} {
		got := strs(WorkflowLines([]byte(tc.text), tc.exists))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// The parser reads by lines and indentation, so it must not care how the
// file is indented, commented, quoted or spaced: any reformatting of the
// canonical caller parses to the same thing.
func TestParseCallerIsIndifferentToFormatting(t *testing.T) {
	want := ParseCaller([]byte(canonicalCaller))
	check(t, func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		indent := strings.Repeat(" ", 1+r.Intn(6))
		var out []string
		for _, line := range strings.Split(canonicalCaller, "\n") {
			if r.Intn(4) == 0 {
				out = append(out, "")
			}
			if r.Intn(4) == 0 {
				out = append(out, strings.Repeat(" ", r.Intn(8))+"# a comment "+strings.Repeat("#", r.Intn(3)))
			}
			trimmed := strings.TrimLeft(line, " ")
			depth := (len(line) - len(trimmed)) / 2
			key, value, found := strings.Cut(trimmed, ": ")
			if found && r.Intn(3) == 0 && !strings.Contains(value, "{") && !strings.Contains(value, "[") {
				q := []string{`"`, `'`}[r.Intn(2)]
				trimmed = key + ":" + strings.Repeat(" ", 1+r.Intn(3)) + q + value + q
			}
			if found && r.Intn(3) == 0 {
				trimmed += strings.Repeat(" ", r.Intn(3)) + " # trailing"
			}
			out = append(out, strings.Repeat(indent, depth)+trimmed+strings.Repeat(" ", r.Intn(3)))
		}
		got := ParseCaller([]byte(strings.Join(out, "\n")))
		return reflect.DeepEqual(got, want)
	})
}

func strs(lines []Line) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.String())
	}
	return out
}
