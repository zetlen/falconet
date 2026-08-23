package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"testing/quick"

	"github.com/zetlen/falconet/internal/doctor"
)

const maxCount = 5000

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// --- step 6: the labels ----------------------------------------------------------

func TestLabelsCreatesWhatIsMissingInReadmeOrder(t *testing.T) {
	want := doctor.Labels{Queue: "infra-request", NeedsInfo: "needs-info", Human: "ready-for-human", PR: "needs-plan-review"}
	for _, tc := range []struct {
		existing []string
		create   []string
	}{
		{nil, []string{"infra-request", "needs-info", "ready-for-human", "needs-plan-review"}},
		{[]string{"infra-request", "needs-info", "ready-for-human", "needs-plan-review"}, nil},
		{[]string{"infra-request", "ready-for-human", "needs-plan-review"}, []string{"needs-info"}},
		{[]string{"bug", "needs-info"}, []string{"infra-request", "ready-for-human", "needs-plan-review"}},
	} {
		steps := Labels(want, tc.existing)
		if len(steps) != 4 {
			t.Fatalf("%v: %d steps", tc.existing, len(steps))
		}
		var got []string
		for i, s := range steps {
			if s.Name != want.Names()[i] {
				t.Errorf("%v: step %d is %s, want %s", tc.existing, i, s.Name, want.Names()[i])
			}
			if s.Create != nil {
				got = append(got, s.Create.Name)
				if s.Create.Name != s.Name || s.Create.Color == "" || s.Create.Description == "" {
					t.Errorf("%v: label %+v is not fully described", tc.existing, *s.Create)
				}
			}
		}
		if !reflect.DeepEqual(got, tc.create) {
			t.Errorf("%v: creates %v, want %v", tc.existing, got, tc.create)
		}
	}
}

func TestTwoKeysNamingOneLabelCreateItOnce(t *testing.T) {
	want := doctor.Labels{Queue: "same", NeedsInfo: "same", Human: "h", PR: "p"}
	steps := Labels(want, nil)
	created := 0
	for _, s := range steps {
		if s.Create != nil && s.Name == "same" {
			created++
		}
	}
	if created != 1 {
		t.Errorf("'same' created %d times", created)
	}
}

func TestEveryLabelColourIsSixHexDigits(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{6}$`)
	for key, s := range style {
		if !hex.MatchString(s.Color) {
			t.Errorf("%s: colour %q is not what GitHub accepts", key, s.Color)
		}
	}
}

// --- step 7: discovery, sorting, the config ------------------------------------

func TestDiscoverStacks(t *testing.T) {
	for _, tc := range []struct {
		name string
		tree fstest.MapFS
		want []string
	}{
		{"two stacks and a docs directory", fstest.MapFS{
			"dns/main.tf":       {},
			"workspace/main.tf": {},
			"docs/README.md":    {},
		}, []string{"dns", "workspace"}},
		{"nested stacks", fstest.MapFS{
			"envs/prod/main.tf":    {},
			"envs/staging/main.tf": {},
			"envs/README.md":       {},
		}, []string{"envs/prod", "envs/staging"}},
		{"a directory with several .tf files is one stack", fstest.MapFS{
			"dns/main.tf":    {},
			"dns/records.tf": {},
			"dns/guards.tf":  {},
		}, []string{"dns"}},
		{"a stack with a module inside it is two", fstest.MapFS{
			"dns/main.tf":         {},
			"dns/modules/zone.tf": {},
			"dns/z.tf":            {},
		}, []string{"dns", "dns/modules"}},
		{".terraform, .git, node_modules and dot-directories are never stacks", fstest.MapFS{
			"dns/main.tf":                         {},
			"dns/.terraform/modules/x/main.tf":    {},
			".git/main.tf":                        {},
			"node_modules/pkg/main.tf":            {},
			".hidden/main.tf":                     {},
			"site/node_modules/other/main.tf":     {},
			"workspace/.terraform.lock.hcl":       {},
			"workspace/.terraform/providers/x.tf": {},
		}, []string{"dns"}},
		{"a .tf at the root is not a stack", fstest.MapFS{
			"main.tf":     {},
			"dns/main.tf": {},
		}, []string{"dns"}},
		{"only root .tf files is no stacks", fstest.MapFS{
			"main.tf": {},
			"vars.tf": {},
		}, nil},
		{"no .tf anywhere", fstest.MapFS{
			"README.md":      {},
			"docs/x.md":      {},
			"dns/main.tf.j2": {},
		}, nil},
		{"a .tf directory name is not a file", fstest.MapFS{
			"dns/thing.tf/inner.txt": {},
		}, nil},
	} {
		got, err := DiscoverStacks(tc.tree)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The same walk over a real temporary tree, since os.DirFS is what the verb
// hands in.
func TestDiscoverStacksOnDisk(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"dns/main.tf", "workspace/main.tf", "docs/README.md", ".terraform/x/main.tf", "dns/.terraform/providers/p.tf", "main.tf"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DiscoverStacks(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"dns", "workspace"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortStacks(t *testing.T) {
	disc := []string{"dns", "site", "workspace"}
	for _, tc := range []struct {
		name         string
		plan, val    []string
		want         Stacks
		unknown      bool
		unsorted     []string
		unknownNames string
	}{
		{"each named once", []string{"dns"}, []string{"workspace", "site"}, Stacks{[]string{"dns"}, []string{"workspace", "site"}}, false, nil, ""},
		{"all planned", []string{"site", "dns", "workspace"}, nil, Stacks{[]string{"site", "dns", "workspace"}, nil}, false, nil, ""},
		{"all validate-only", nil, disc, Stacks{nil, disc}, false, nil, ""},
		{"a repeat in one list is one", []string{"dns", "dns"}, []string{"site", "workspace"}, Stacks{[]string{"dns"}, []string{"site", "workspace"}}, false, nil, ""},
		{"one in neither", []string{"dns"}, []string{"site"}, Stacks{}, false, []string{"workspace"}, ""},
		{"none named", nil, nil, Stacks{}, false, disc, ""},
		{"a name nothing discovered", []string{"nosuch"}, disc, Stacks{}, true, nil, "nosuch"},
		{"a name in both lists", []string{"dns"}, []string{"dns", "site", "workspace"}, Stacks{}, true, nil, "dns"},
	} {
		got, err := Sort(disc, tc.plan, tc.val)
		var unknown *UnknownStackError
		var unsorted *UnsortedError
		switch {
		case tc.unknown:
			if !errors.As(err, &unknown) {
				t.Errorf("%s: want an UnknownStackError, got %v", tc.name, err)
			} else if !strings.Contains(unknown.Name, tc.unknownNames) || !strings.Contains(err.Error(), "dns, site, workspace") {
				t.Errorf("%s: %v", tc.name, err)
			}
		case tc.unsorted != nil:
			if !errors.As(err, &unsorted) {
				t.Errorf("%s: want an UnsortedError, got %v", tc.name, err)
			} else if !reflect.DeepEqual(unsorted.Names, tc.unsorted) {
				t.Errorf("%s: unsorted %v, want %v", tc.name, unsorted.Names, tc.unsorted)
			}
		default:
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			} else if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
			}
		}
	}
}

func TestSplitList(t *testing.T) {
	for in, want := range map[string][]string{
		"":                   nil,
		"dns":                {"dns"},
		"dns,workspace":      {"dns", "workspace"},
		" dns , workspace/ ": {"dns", "workspace"},
		",,dns,,":            {"dns"},
		"envs/prod/":         {"envs/prod"},
	} {
		if got := SplitList(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitList(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestConfigJSONIsExactlyTheReadmesShape(t *testing.T) {
	got := string(ConfigJSON(Stacks{Plan: []string{"dns"}, ValidateOnly: []string{"workspace", "site"}}))
	want := `{
  "stacks": {
    "plan": [
      "dns"
    ],
    "validate_only": [
      "workspace",
      "site"
    ]
  },
  "prompts": {
    "implement": "prompts/implement.md"
  }
}
`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	// Empty lists are [], never null: the schema reads a list.
	empty := ConfigJSON(Stacks{})
	if !strings.Contains(string(empty), `"plan": []`) || !strings.Contains(string(empty), `"validate_only": []`) {
		t.Errorf("empty stacks: %s", empty)
	}
	var parsed struct {
		Stacks struct {
			Plan         []string `json:"plan"`
			ValidateOnly []string `json:"validate_only"`
		}
		Prompts map[string]string
	}
	if err := json.Unmarshal(empty, &parsed); err != nil || parsed.Stacks.Plan == nil || parsed.Stacks.ValidateOnly == nil || parsed.Prompts["implement"] != PromptPath {
		t.Errorf("the config does not round-trip: %v %+v", err, parsed)
	}
}

// Whatever the lists, the file parses back to the same lists, ends in
// exactly one newline, and is indented two spaces.
func TestConfigJSONRoundTrips(t *testing.T) {
	check(t, func(plan, val []string) bool {
		for i := range plan {
			plan[i] = "p" + plan[i]
		}
		for i := range val {
			val[i] = "v" + val[i]
		}
		out := ConfigJSON(Stacks{Plan: plan, ValidateOnly: val})
		if !bytes.HasSuffix(out, []byte("}\n")) || bytes.HasSuffix(out, []byte("\n\n")) {
			return false
		}
		var parsed struct {
			Stacks struct {
				Plan         []string `json:"plan"`
				ValidateOnly []string `json:"validate_only"`
			} `json:"stacks"`
		}
		if err := json.Unmarshal(out, &parsed); err != nil {
			return false
		}
		return reflect.DeepEqual(parsed.Stacks.Plan, orEmpty(plan)) && reflect.DeepEqual(parsed.Stacks.ValidateOnly, orEmpty(val))
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- step 8: the workflow ----------------------------------------------------------

func TestWorkflowPinsTheUsesLineAndNothingElseNamesARef(t *testing.T) {
	for _, ref := range []string{"main", "v0.1.0", "0123abcd"} {
		wf := string(Workflow(ref))
		want := "    uses: zetlen/falconet/.github/workflows/falconet.yml@" + ref + "\n"
		if strings.Count(wf, want) != 1 {
			t.Errorf("%s: the uses: line is not exactly once:\n%s", ref, wf)
		}
		if strings.Contains(wf, "falconet-ref") {
			t.Errorf("%s: the post-cutover caller has no falconet-ref input", ref)
		}
		if strings.Contains(wf, "<VERSION>") {
			t.Errorf("%s: the placeholder survived", ref)
		}
		c := doctor.ParseCaller([]byte(wf))
		if !c.HasUses || c.Ref != ref {
			t.Errorf("%s: doctor reads the caller as %+v", ref, c)
		}
		for _, secret := range []string{"app-id: ${{ secrets.FALCONET_APP_ID }}", "app-private-key: ${{ secrets.FALCONET_APP_PRIVATE_KEY }}",
			"anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}", "plan-env: ${{ secrets.FALCONET_PLAN_ENV }}"} {
			if !strings.Contains(wf, "      "+secret+"\n") {
				t.Errorf("%s: no %q", ref, secret)
			}
		}
		if !strings.Contains(wf, "concurrency:\n  group: falconet-${{ github.event.issue.number }}\n  cancel-in-progress: false\n") {
			t.Errorf("%s: the concurrency block is not the README's", ref)
		}
	}
	if Uses("v1") != "zetlen/falconet/.github/workflows/falconet.yml@v1" {
		t.Error(Uses("v1"))
	}
}

// The permissions block is what contract.test.sh derives from falconet.yml
// — `write` at job-permission indentation is the widest a job asks for,
// else read — read the same way, so the two cannot drift: a caller that
// grants less than a job declares is a startup_failure with no jobs, no
// logs and nothing on the issue.
func TestWorkflowPermissionsAreWhatTheWidestJobDeclares(t *testing.T) {
	reusable, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "falconet.yml"))
	if err != nil {
		t.Skipf("no falconet.yml beside this module: %v", err)
	}
	widest := func(perm string) string {
		for _, line := range strings.Split(string(reusable), "\n") {
			if line == "      "+perm+": write" {
				return "write"
			}
		}
		return "read"
	}
	c := doctor.ParseCaller(Workflow("main"))
	if !c.HasPermissions || c.Inline != "" {
		t.Fatalf("the template has no permissions block: %+v", c)
	}
	got := map[string]string{}
	for _, g := range c.Grants {
		got[g.Scope] = g.Level
	}
	for _, perm := range []string{"contents", "issues", "pull-requests"} {
		if got[perm] != widest(perm) {
			t.Errorf("the template grants %s: %s, and the widest job declares %s", perm, got[perm], widest(perm))
		}
	}
	if len(c.Grants) != 3 {
		t.Errorf("the template grants %v, and step 8 is three lines", c.Grants)
	}
	// And it is doctor's RequiredPermissions, which is what doctor checks a
	// caller against: a template doctor would fault is not a template.
	for _, l := range doctor.WorkflowLines(Workflow("v0.1.0"), true) {
		if l.Status == doctor.Missing {
			t.Errorf("doctor faults the template: %s", l)
		}
	}
}

// The README's step 8 block, read the way contract.test.sh reads it, is
// the template's block: the README is the specification, and the two say
// the same thing or one of them is wrong.
func TestWorkflowPermissionsMatchTheReadmesStep8(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Skipf("no README beside this module: %v", err)
	}
	var caller []string
	in := false
	for _, line := range strings.Split(string(readme), "\n") {
		if strings.HasPrefix(line, "### 8.") {
			in = true
		} else if strings.HasPrefix(line, "### 9.") {
			break
		}
		if in {
			caller = append(caller, line)
		}
	}
	var perms []string
	in = false
	for _, line := range caller {
		switch {
		case strings.HasPrefix(line, "permissions:"):
			in = true
		case in && line != "" && !strings.HasPrefix(line, " "):
			in = false
		case in:
			perms = append(perms, line)
		}
	}
	if len(perms) == 0 {
		t.Fatal("README step 8 has no permissions block")
	}
	wf := string(Workflow("main"))
	block := "permissions:\n" + strings.Join(perms, "\n") + "\n"
	if !strings.Contains(wf, block) {
		t.Errorf("the template's permissions block is not the README's:\n%s", block)
	}
}

func TestWorkflowRef(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "main",
		"dev":                                  "main",
		"(devel)":                              "main",
		"v0.1.0":                               "v0.1.0",
		"v1.2.3":                               "v1.2.3",
		"v0.0.0-20260822120000-0123456789ab":   "main",
		"v0.1.1-0.20260822120000-0123456789ab": "main",
		"v0.1.1-0.20260822120000-0123456789ab+dirty": "main",
		"v0.2.0-pre.0.20260822120000-abcdefabcdef":   "main",
		"v0.2.0-rc1": "v0.2.0-rc1",
	} {
		if got := WorkflowRef(in); got != want {
			t.Errorf("WorkflowRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- step 2: the .gitignore line --------------------------------------------------

func TestAppendIgnore(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in, out string
		changed bool
	}{
		{"no file", "", ".falconet/\n", true},
		{"a file ending in a newline", "node_modules/\n", "node_modules/\n.falconet/\n", true},
		{"a file not ending in a newline", "node_modules/", "node_modules/\n.falconet/\n", true},
		{"already there", "node_modules/\n.falconet/\n", "node_modules/\n.falconet/\n", false},
		{"already there with spaces", "  .falconet/  \n", "  .falconet/  \n", false},
		{"already there in the middle", "a\n.falconet/\nb\n", "a\n.falconet/\nb\n", false},
		{"a similar line is not it", ".falconet\n", ".falconet\n.falconet/\n", true},
		{"a commented line is not it", "# .falconet/\n", "# .falconet/\n.falconet/\n", true},
	} {
		out, changed := AppendIgnore([]byte(tc.in), ".falconet/")
		if string(out) != tc.out || changed != tc.changed {
			t.Errorf("%s: got %q %v, want %q %v", tc.name, out, changed, tc.out, tc.changed)
		}
	}
}

// Applying it twice is applying it once, for any file and any entry: the
// second application changes nothing and returns the same bytes.
func TestAppendIgnoreIsIdempotent(t *testing.T) {
	check(t, func(content []byte, dir string) bool {
		entry := IgnoreEntry(strings.ReplaceAll(strings.TrimSpace(dir), "\n", "") + "x")
		once, changed := AppendIgnore(content, entry)
		twice, changedAgain := AppendIgnore(once, entry)
		if changedAgain || !bytes.Equal(once, twice) {
			return false
		}
		// And the entry is a line of its own after the first application.
		found := false
		for _, line := range strings.Split(string(once), "\n") {
			if line == entry {
				found = true
			}
		}
		return found && (changed || bytes.Equal(once, content))
	})
}

func TestIgnoreEntry(t *testing.T) {
	for in, want := range map[string]string{".falconet": ".falconet/", ".falconet/": ".falconet/", "scratch": "scratch/"} {
		if got := IgnoreEntry(in); got != want {
			t.Errorf("IgnoreEntry(%q) = %q", in, got)
		}
	}
}

// --- the commit and the report --------------------------------------------------------

func TestCommitMessageNamesEveryFile(t *testing.T) {
	msg := string(CommitMessage([]Written{{".gitignore", "ignores .falconet/"}, {".github/falconet.json", "the stacks"}}))
	if !strings.HasPrefix(msg, CommitSubject+"\n\n") {
		t.Errorf("the subject is not first: %q", msg)
	}
	for _, want := range []string{".gitignore", "ignores .falconet/", ".github/falconet.json", "the stacks"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the body lacks %q:\n%s", want, msg)
		}
	}
	if !strings.HasSuffix(msg, "\n") || strings.HasSuffix(msg, "\n\n") {
		t.Errorf("the message ends badly: %q", msg)
	}
}

func TestSummaryCountsEveryStatusButNotes(t *testing.T) {
	r := doctor.Report{
		{Status: doctor.OK}, {Status: doctor.Done}, {Status: doctor.Done}, {Status: doctor.Skipped},
		{Status: doctor.Missing}, {Status: doctor.CannotTell}, {Status: doctor.Note},
	}
	if got := Summary(r); got != "init: 1 ok, 2 done, 1 skipped, 1 missing, 1 cannot tell" {
		t.Error(got)
	}
}

func TestLeftForYouIsNumberedInOrder(t *testing.T) {
	if LeftForYou(nil) != "" {
		t.Error("an empty list prints a block")
	}
	got := LeftForYou([]string{"git push origin main", LeftCanary, LeftDoctor})
	want := "\nLeft for you:\n  1. git push origin main\n  2. " + LeftCanary + "\n  3. " + LeftDoctor + "\n"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if LeftPush("") == LeftPush("main") || !strings.Contains(LeftPush("main"), "git push origin main") {
		t.Error(LeftPush(""), LeftPush("main"))
	}
	fix := LeftFix(doctor.Line{Status: doctor.Missing, Step: 1, Text: "the repository has issues disabled", Hint: "enable them"})
	if fix != "step 1 — the repository has issues disabled: enable them" {
		t.Error(fix)
	}
}
