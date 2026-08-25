package validate

import (
	"bytes"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

const maxCount = 10000

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// --- the smuggle guard ----------------------------------------------------------

func TestSmuggledTable(t *testing.T) {
	cases := []struct {
		name    string
		changed []string
		dir     string
		want    []string
	}{
		{"a file under the handoff directory",
			[]string{"dns/a.tf", ".falconet/plan.txt"}, ".falconet", []string{".falconet/plan.txt"}},
		{"deeper under it",
			[]string{".falconet/sub/x"}, ".falconet", []string{".falconet/sub/x"}},
		{"the directory's name itself, as a file",
			[]string{".falconet"}, ".falconet", []string{".falconet"}},
		{"a path that merely starts with the name is not inside it",
			[]string{".falconetx/a", ".falconet-old/b", ".falconet.bak"}, ".falconet", nil},
		{"a path that contains the name elsewhere is not inside it",
			[]string{"dns/.falconet/x", "a/.falconet"}, ".falconet", nil},
		{"every hit, in git's order",
			[]string{".falconet/b", "dns/a.tf", ".falconet/a", "site/c.tf"}, ".falconet",
			[]string{".falconet/b", ".falconet/a"}},
		{"nothing changed", nil, ".falconet", nil},
		// The name is a literal, never a pattern: the regex-shaped names that
		// broke the ERE, and names whose metacharacters would otherwise match
		// more or less than themselves.
		{"a regex-shaped name is matched literally",
			[]string{"ci(handoff)/plan.txt"}, "ci(handoff)", []string{"ci(handoff)/plan.txt"}},
		{"and does not match what its regex would have",
			[]string{"cihandoff/plan.txt", "ci/plan.txt"}, "ci(handoff)", nil},
		{"a bracket in the name",
			[]string{"[out]/a", "o/a"}, "[out]", []string{"[out]/a"}},
		{"a dot in the name is a dot",
			[]string{".falconet/a", "xfalconet/a"}, ".falconet", []string{".falconet/a"}},
		{"a star in the name is a star",
			[]string{"*/a", "dns/a"}, "*", []string{"*/a"}},
		// git's quoted form: the report names the path as git printed it.
		{"a quoted path under the directory is refused and named quoted",
			[]string{`".falconet/a\"b"`}, ".falconet", []string{`".falconet/a\"b"`}},
		{"a quoted path with a tab",
			[]string{`".falconet/a\tb.txt"`}, ".falconet", []string{`".falconet/a\tb.txt"`}},
		{"a quoted path elsewhere is not",
			[]string{`"dns/a\"b.tf"`}, ".falconet", nil},
		{"a quoted name of the directory itself",
			[]string{`".falconet"`}, ".falconet", []string{`".falconet"`}},
		// An empty name holds nothing.
		{"an empty name refuses nothing", []string{"a", "/a", ""}, "", nil},
	}
	for _, c := range cases {
		got := Smuggled(c.changed, c.dir)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Smuggled(%q, %q) = %q, want %q", c.name, c.changed, c.dir, got, c.want)
		}
	}
}

// The guard is a prefix test and nothing else: for any name and any path,
// the path is refused exactly when, quotes stripped, it IS the name or
// starts with the name and a slash — regardless of what characters the name
// holds. Metacharacters cannot make it fail open or closed.
func TestSmuggledIsExactlyAPrefixTest(t *testing.T) {
	check(t, func(name, path string, quoted bool) bool {
		if name == "" || strings.ContainsRune(name, '\n') || strings.ContainsRune(path, '\n') {
			return true
		}
		p := path
		if quoted {
			p = `"` + path + `"`
		}
		want := path == name || strings.HasPrefix(path, name+"/")
		got := Smuggled([]string{p}, name)
		if want {
			return len(got) == 1 && got[0] == p
		}
		return len(got) == 0
	})
}

// TestSmuggledAgreesWithBash holds the guard to the loop it replaced: the
// same pairs through `[[ "$_u" == "$HANDOFF_DIR" || "$_u" == "$HANDOFF_DIR"/* ]]`
// with the bash's own quote stripping, in one bash process. Skipped where
// there is no bash.
func TestSmuggledAgreesWithBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to differ against")
	}
	const alphabet = "ab./\"(*[]\\-x"
	r := rand.New(rand.NewSource(20260822))
	const n = 5000
	names := make([]string, n)
	paths := make([]string, n)
	var stdin bytes.Buffer
	for i := range names {
		names[i] = random(r, alphabet, 1+r.Intn(5))
		if r.Intn(2) == 0 {
			paths[i] = names[i] + random(r, alphabet, r.Intn(4))
		} else {
			paths[i] = random(r, alphabet, r.Intn(8))
		}
		stdin.WriteString(names[i])
		stdin.WriteByte(0)
		stdin.WriteString(paths[i])
		stdin.WriteByte(0)
	}
	script := `while IFS= read -r -d '' HANDOFF_DIR && IFS= read -r -d '' _p; do
  _u="${_p%\"}"; _u="${_u#\"}"
  if [[ "$_u" == "$HANDOFF_DIR" || "$_u" == "$HANDOFF_DIR"/* ]]; then printf 1; else printf 0; fi
done`
	cmd := exec.Command(bash, "-c", script)
	cmd.Stdin = &stdin
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if len(out) != n {
		t.Fatalf("bash answered %d of %d pairs", len(out), n)
	}
	disagreements, matches := 0, 0
	for i := range names {
		want := out[i] == '1'
		if want {
			matches++
		}
		got := len(Smuggled([]string{paths[i]}, names[i])) == 1
		if got != want {
			disagreements++
			if disagreements <= 20 {
				t.Errorf("name %q path %q: go %v, bash %v", names[i], paths[i], got, want)
			}
		}
	}
	t.Logf("%d pairs, %d matches, %d disagreements", n, matches, disagreements)
	if matches < n/20 {
		t.Errorf("only %d of %d pairs matched; the generator is not exercising the refusal", matches, n)
	}
}

func random(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

// --- the report, byte for byte -----------------------------------------------------

func TestReports(t *testing.T) {
	base := "0123456789abcdef0123456789abcdef01234567"
	if got, want := ReportNoCommit(base), "## No commit on the working branch\n"+
		"\n"+
		"HEAD is still "+base+" — the commit this run started from — so nothing\n"+
		"was recorded for this request. There is nothing to validate, plan\n"+
		"or review.\n"+
		"\n"+
		"Nothing about the request caused this. The step that makes the\n"+
		"commit reported that it had made one, and then there was none, so\n"+
		"the fault is in the pipeline. The run log linked above has the\n"+
		"whole story; someone should read it before this request is tried\n"+
		"again.\n"; got != want {
		t.Errorf("ReportNoCommit:\n%q\nwant\n%q", got, want)
	}

	if got, want := ReportSmuggled(".falconet", []string{".falconet/plan.txt", `".falconet/a\"b"`}),
		"## The commit contains CI's own handoff files\n"+
			"\n"+
			"These committed paths are inside .falconet/:\n"+
			"\n"+
			"  .falconet/plan.txt\n"+
			"  \".falconet/a\\\"b\"\n"+
			"\n"+
			".falconet/ is where each stage of this pipeline leaves files for\n"+
			"the next one — the request, the plan, the diff, this report. It is\n"+
			"listed in .gitignore and it is not part of any change. Committing it\n"+
			"would ship CI's internals in the pull request and would hand the\n"+
			"reviewing stage its own notes as part of the change to review.\n"+
			"\n"+
			"Nothing about the request caused this either. These paths are\n"+
			"ignored, so only a deliberate force-add can commit them, and the\n"+
			"only thing that stages files in this pipeline is a script that\n"+
			"names every path it stages. Something upstream is wrong. The\n"+
			"branch and the run log linked above have the rest.\n"; got != want {
		t.Errorf("ReportSmuggled:\n%q\nwant\n%q", got, want)
	}

	for name, got := range map[string]string{
		"stack missing":      SectionStackMissing("nowhere", "config .stacks.plan names \"nowhere\", which is not a directory in /r. Set .stacks.plan in .github/falconet.json to the directories your OpenTofu stacks live in."),
		"validate failed":    SectionValidateFailed("site", []byte("Error: Unsupported argument\n  on site/main.tf line 3\n")),
		"plan not attempted": SectionPlanNotAttempted(),
		"plan failed":        SectionPlanFailed("dns", []byte("Error: Resource precondition failed\n"), []byte("OpenTofu will perform the following actions.\n\n  # dns_record.example will be created\n")),
		"plan heading":       PlanHeading("dns"),
	} {
		want := map[string]string{
			"stack missing":      "## the configured stack nowhere/ is not in this repository\n\nconfig .stacks.plan names \"nowhere\", which is not a directory in /r. Set .stacks.plan in .github/falconet.json to the directories your OpenTofu stacks live in.\n\n",
			"validate failed":    "## tofu validate failed (site/)\n\nError: Unsupported argument\n  on site/main.tf line 3\n\n",
			"plan not attempted": "## tofu plan was not attempted\n\n`tofu validate` failed above, so a plan would only repeat it.\n\n",
			"plan failed":        "## tofu plan failed (dns/)\n\nError: Resource precondition failed\n\n### plan output before the failure\n\nOpenTofu will perform the following actions.\n\n  # dns_record.example will be created\n\n",
			"plan heading":       "## dns\n\n",
		}[name]
		if got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}

	// The promise the header makes: the report instructs nobody. The one
	// sentence that broke it lives on stderr, and none of the report's text
	// carries it.
	for name, section := range map[string]string{
		"no commit": ReportNoCommit("x"), "smuggled": ReportSmuggled("d", []string{"d/a"}),
		"plan failed":   SectionPlanFailed("s", []byte("Error: precondition\n"), nil),
		"not attempted": SectionPlanNotAttempted(),
	} {
		if strings.Contains(section, "never weaken it") || strings.Contains(section, "quote it") {
			t.Errorf("%s carries the agent-facing sentence", name)
		}
	}
	if !strings.HasSuffix(GuardNote, "\n") || strings.Count(GuardNote, "\n") != 2 {
		t.Errorf("GuardNote is two lines: %q", GuardNote)
	}
}

// --- the coverage sections (#23) ------------------------------------------------

func TestCoverageSections(t *testing.T) {
	uncovered := SectionUncovered(
		[]string{"talaria-gcp/variables.tf", "talaria-gcp/main.tf"},
		[]string{"dns", "workspace", "site"}, true, ".github/falconet.json")
	for _, want := range []string{
		"## the change is in no stack this repository knows about",
		// The files, so the requester can see WHICH change went nowhere.
		"  talaria-gcp/variables.tf\n  talaria-gcp/main.tf",
		// The stacks, so they can see the gap rather than infer it.
		"The stacks this run checked:\n\n  dns\n  workspace\n  site\n",
		".stacks.plan",
		"Nothing about the request caused this",
	} {
		if !strings.Contains(uncovered, want) {
			t.Errorf("SectionUncovered lacks %q:\n%s", want, uncovered)
		}
	}

	// A repository that declared nothing gets a different middle: there is
	// no config key to add the directory to, because nothing named any.
	discovered := SectionUncovered([]string{"main.tf"}, []string{"dns"}, false, ".github/falconet.json")
	if strings.Contains(discovered, "named in neither") {
		t.Errorf("an undeclared layout is told to edit a list nothing set:\n%s", discovered)
	}
	if !strings.Contains(discovered, "never plans the tree it stands in") {
		t.Errorf("SectionUncovered does not say why a root .tf is in no stack:\n%s", discovered)
	}

	unplanned := SectionUnplanned([]string{"workspace"}, []string{"dns"}, true, ".github/falconet.json")
	for _, want := range []string{
		"## nothing this change touches is planned",
		"The change reaches:\n\n  workspace\n",
		"This repository plans:\n\n  dns\n",
		"Nothing about the request caused this",
	} {
		if !strings.Contains(unplanned, want) {
			t.Errorf("SectionUnplanned lacks %q:\n%s", want, unplanned)
		}
	}

	// A change that reaches nothing at all is the empty list, and "none" is
	// the fact the reader needs — not a heading over nothing.
	if got := SectionUnplanned(nil, []string{"dns"}, true, "c.json"); !strings.Contains(got, "The change reaches: none.") {
		t.Errorf("an empty list is not said in words:\n%s", got)
	}

	// Both sections end as every other one does: a blank line before the
	// next, since the report is appended to section by section.
	for name, section := range map[string]string{"uncovered": uncovered, "unplanned": unplanned} {
		if !strings.HasSuffix(section, "\n\n") {
			t.Errorf("%s does not end in a blank line: %q", name, section[len(section)-8:])
		}
		// The report instructs nobody, as the header promises: the reader is
		// the requester, who is not the person being told what to do about
		// a config they may not be able to edit.
		if strings.Contains(section, "You should") || strings.Contains(section, "never weaken it") {
			t.Errorf("%s instructs its reader", name)
		}
	}
}

func TestCoverageRunLogLines(t *testing.T) {
	if got := UncoveredLine([]string{"a.tf", "b/c.tf"}); got != "changed files in no stack: a.tf b/c.tf" {
		t.Errorf("UncoveredLine = %q", got)
	}
	if got := UnplannedLine([]string{"workspace", "site"}); got != "nothing planned: the change reaches workspace, site" {
		t.Errorf("UnplannedLine = %q", got)
	}
	if got := PlannedLine([]string{"dns", "workspace"}); got != "planning: dns, workspace" {
		t.Errorf("PlannedLine = %q", got)
	}
	if got := PlannedLine(nil); got != "planning: nothing (this repository plans no stacks)" {
		t.Errorf("PlannedLine of nothing = %q", got)
	}
}

// --- the run log ----------------------------------------------------------------

func TestRunLogLines(t *testing.T) {
	if got := CommitLine("abc1234", "0123456789abcdef"); got != "commit: abc1234 on top of 0123456" {
		t.Errorf("CommitLine = %q", got)
	}
	if got := CommitLine("abc1234", "short"); got != "commit: abc1234 on top of short" {
		t.Errorf("CommitLine with a short base = %q", got)
	}
	if got := ValidateOK("dns"); got != "tofu validate (dns/): OK" {
		t.Errorf("ValidateOK = %q", got)
	}
	plan := []byte("OpenTofu will perform the following actions.\n\n  # x\n\nPlan: 1 to add, 0 to change, 0 to destroy.\n")
	if got := PlanOK("dns", plan); got != "tofu plan (dns/): OK (5 lines)" {
		t.Errorf("PlanOK = %q", got)
	}
	if got := PlanOK("dns", nil); got != "tofu plan (dns/): OK (0 lines)" {
		t.Errorf("PlanOK of nothing = %q", got)
	}
	if got := PlanOK("dns", []byte("no newline")); got != "tofu plan (dns/): OK (0 lines)" {
		t.Errorf("wc -l counts newlines, not lines: %q", got)
	}
	if PlanBegin("dns") != "----- begin tofu plan (dns/) -----" || PlanEnd("dns") != "----- end tofu plan (dns/) -----" {
		t.Error("the plan markers")
	}
}

// Indent is `sed 's/^/  /'`, pinned to the measured answers and held for
// any input: every line gains two spaces, nothing else changes, and the
// trailing newline is exactly what it was.
func TestIndent(t *testing.T) {
	for in, want := range map[string]string{
		"":             "",
		"a\n":          "  a\n",
		"a\nb\n":       "  a\n  b\n",
		"a":            "  a",
		"a\nb":         "  a\n  b",
		"\n":           "  \n",
		"\n\n":         "  \n  \n",
		"  already\n":  "    already\n",
		"dns/a b.tf\n": "  dns/a b.tf\n",
	} {
		if got := Indent(in); got != want {
			t.Errorf("Indent(%q) = %q, want %q", in, got, want)
		}
	}
	check(t, func(s string) bool {
		got := Indent(s)
		if s == "" {
			return got == ""
		}
		want := "  " + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n  ")
		if strings.HasSuffix(s, "\n") {
			want += "\n"
		}
		return got == want
	})
}

// TestIndentAgreesWithSed holds Indent to whatever sed this machine has —
// BSD on a Mac, GNU on the runners — over random text, with and without a
// final newline. Where the two seds differ (BSD adds a newline a partial
// last line never had), the port keeps GNU's answer, and that one input
// shape is set aside here.
func TestIndentAgreesWithSed(t *testing.T) {
	sed, err := exec.LookPath("sed")
	if err != nil {
		t.Skip("no sed to differ against")
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		text := random(r, "ab \n/.", r.Intn(30))
		if text != "" && !strings.HasSuffix(text, "\n") {
			continue
		}
		cmd := exec.Command(sed, "s/^/  /")
		cmd.Stdin = strings.NewReader(text)
		want, err := cmd.Output()
		if err != nil {
			t.Fatalf("sed: %v", err)
		}
		if got := Indent(text); got != string(want) {
			t.Errorf("Indent(%q) = %q, sed says %q", text, got, want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	for in, want := range map[string][]string{
		"":                     nil,
		"a\n":                  {"a"},
		"a\nb\n":               {"a", "b"},
		"a\n\nb\n":             {"a", "b"},
		"a\nb":                 {"a", "b"},
		"\"q\\\"x\"\ndns/a\n":  {"\"q\\\"x\"", "dns/a"},
		".falconet/plan.txt\n": {".falconet/plan.txt"},
	} {
		if got := SplitLines([]byte(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitLines(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLines(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 0, "a\n": 1, "a\nb\n": 2, "\n\n\n": 3, "a\nb": 1} {
		if got := Lines([]byte(in)); got != want {
			t.Errorf("Lines(%q) = %d, want %d", in, got, want)
		}
	}
}
