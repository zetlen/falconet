package commit

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/zetlen/falconet/internal/config"
)

// --- the allowlist's globs ---------------------------------------------------

func TestAllowPatternTable(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		// The README's sentence, both halves.
		{"*.tf", "dns/records.tf", true},
		{"*.tf", "records.tf", true},
		{"*.tf", "dns/zones/a.tf", true},
		{"dns/*.tf", "site/a.tf", false},
		{"dns/*.tf", "dns/a.tf", true},
		{"dns/*.tf", "dns/zones/a.tf", true},
		{"*.tf", "a.tfvars", false},
		{"*.tf", ".hidden.tf", true},
		// `?` is one character.
		{"?.tf", "a.tf", true},
		{"?.tf", "ab.tf", false},
		{"?", ".", true},
		// Bracket expressions.
		{"[ab].tf", "a.tf", true},
		{"[ab].tf", "c.tf", false},
		{"[!ab].tf", "c.tf", true},
		{"[^ab].tf", "a.tf", false},
		{"[a-c]*.tf", "ab.tf", true},
		{"[]a]", "]", true},
		{"[!]a]", "]", false},
		{"[!]a]", "b", true},
		{"[a-]", "-", true},
		{"[[:alpha:]]", "q", true},
		{"[[:alpha:]]", "1", false},
		{`[\]]`, "]", true},
		{`[a\-z]`, "-", true},
		{`[a\-z]`, "b", false},
		{"[[]", "[", true},
		// An unterminated `[` is a literal `[`.
		{"[a", "[a", true},
		{"x[", "x[", true},
		{"a]", "a]", true},
		// Backslash quotes the next character. Alone at the end it is
		// refused; see TestUnreadableGlobsAreRefusedLoudly.
		{`a\*.tf`, "a*.tf", true},
		{`a\*.tf`, "ab.tf", false},
		{`a\\b`, `a\b`, true},
		{`a\\`, `a\`, true},
		// `|` is a character, not alternation.
		{"a|b", "a|b", true},
		{"a|b", "a", false},
		// Regexp metacharacters are literal in a glob.
		{"a.tf", "aXtf", false},
		{"a+b", "a+b", true},
		{"(x)", "(x)", true},
		{"$x^", "$x^", true},
		{"a{1,2}", "a{1,2}", true},
		// Empty matches empty.
		{"", "", true},
		{"", "a", false},
		// Anchored.
		{"a", "ab", false},
		{"b", "ab", false},
		{"*", "", true},
	}
	for _, c := range cases {
		re, err := AllowPattern(c.glob)
		if err != nil {
			t.Errorf("AllowPattern(%q): %v", c.glob, err)
			continue
		}
		if got := re.MatchString(c.path); got != c.want {
			t.Errorf("%q ~ %q: got %v, want %v (regexp %s)", c.path, c.glob, got, c.want, re)
		}
	}
}

// TestUnreadableGlobsAreRefusedLoudly: the two places the port parts
// company with bash are both refusals. A reversed range matched nothing in
// every bash, silently. A trailing unpaired backslash matched nothing in bash
// 3.2 and, in bash 5, a literal backslash after a character but nothing
// after a star. Neither is a rule an operator meant to write.
func TestUnreadableGlobsAreRefusedLoudly(t *testing.T) {
	for _, glob := range []string{"[c-a]", `\`, `a\`, `*\`, `a\\\`} {
		if _, err := AllowPattern(glob); err == nil {
			t.Errorf("AllowPattern(%q) compiled; it must refuse", glob)
		}
		if _, err := NewPolicy([]string{"*.tf", glob}, nil); err == nil {
			t.Errorf("NewPolicy accepted %q", glob)
		}
	}
}

// patternAlphabet is what the random globs and paths are made of: letters,
// the separators, and every character the translation has an arm for.
const patternAlphabet = "ab./*?[]!^-\\|t"

// globbish is a random glob over the alphabet.
type globbish string

func (globbish) Generate(r *rand.Rand, size int) reflect.Value {
	return reflect.ValueOf(globbish(randomOver(r, patternAlphabet, r.Intn(7))))
}

func randomOver(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

// derive builds a path from a glob by filling its wildcards, so that a useful
// share of the pairs match. Half the time it is random instead.
func derive(r *rand.Rand, glob string) string {
	if r.Intn(2) == 0 {
		return randomOver(r, "ab./t\\[]-|", r.Intn(7))
	}
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			b.WriteString(randomOver(r, "ab./t", r.Intn(4)))
		case '?':
			b.WriteString(randomOver(r, "ab./t", 1))
		case '[', ']', '!', '^', '\\':
			b.WriteString(randomOver(r, "ab-]\\", 1))
		default:
			b.WriteByte(glob[i])
		}
	}
	return b.String()
}

// TestAllowPatternAgreesWithBash is the differential: thousands of random
// (glob, path) pairs, matched by the translation here and by an actual bash
// `case`, in one bash process. The `case` is the original, and this is the
// port; the suite's fixtures cover a handful of globs and this covers the
// corners of the syntax nobody would think to write a fixture for. Skipped
// where there is no bash.
func TestAllowPatternAgreesWithBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to differ against")
	}
	r := rand.New(rand.NewSource(20260822))
	const n = 20000
	globs := make([]string, n)
	paths := make([]string, n)
	var stdin bytes.Buffer
	for i := range globs {
		globs[i] = string(globbish("").Generate(r, 0).Interface().(globbish))
		paths[i] = derive(r, globs[i])
		stdin.WriteString(globs[i])
		stdin.WriteByte(0)
		stdin.WriteString(paths[i])
		stdin.WriteByte(0)
	}
	script := `while IFS= read -r -d '' p && IFS= read -r -d '' w; do
  case "$w" in $p) printf 1 ;; *) printf 0 ;; esac
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
	// A pattern ending in an unpaired backslash is the one place bash
	// disagrees with itself (bash 3.2: nothing; bash 5: a literal backslash,
	// or nothing after a star), and the port refuses it. Those pairs are set
	// aside from the comparison, counted, and held to the refusal. Every
	// other refusal must be a pattern bash matched nothing with.
	disagreements, refused, matches, setAside := 0, 0, 0, 0
	for i := range globs {
		re, err := AllowPattern(globs[i])
		if strings.HasSuffix(globs[i], `\`) && (len(globs[i])-len(strings.TrimRight(globs[i], `\`)))%2 == 1 {
			setAside++
			if err == nil {
				t.Errorf("%q ends in an unpaired backslash and compiled", globs[i])
			}
			continue
		}
		want := out[i] == '1'
		if want {
			matches++
		}
		if err != nil {
			refused++
			if want {
				t.Errorf("%q refused to compile, but bash matched %q with it", globs[i], paths[i])
			}
			continue
		}
		if got := re.MatchString(paths[i]); got != want {
			disagreements++
			if disagreements <= 20 {
				t.Errorf("%q ~ %q: go %v, bash %v (regexp %s)", paths[i], globs[i], got, want, re)
			}
		}
	}
	t.Logf("%d pairs, %d matches, %d refused to compile, %d disagreements, %d set aside for an unpaired trailing backslash",
		n, matches, refused, disagreements, setAside)
	if matches < n/20 {
		t.Errorf("only %d of %d pairs matched; the generator is not exercising the match arm", matches, n)
	}
}

func TestPathAllowedIsAny(t *testing.T) {
	p, err := NewPolicy([]string{"", "*.tf", "docs/*.md", ""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Allow, []string{"*.tf", "docs/*.md"}) {
		t.Errorf("empty entries were not dropped: %q", p.Allow)
	}
	for path, want := range map[string]bool{
		"dns/records.tf":                     true,
		"docs/note.md":                       true,
		"docs/a/b.md":                        true,
		"AGENTS.md":                          false,
		".github/workflows/infra-issues.yml": false,
	} {
		if got := p.PathAllowed(path); got != want {
			t.Errorf("PathAllowed(%q) = %v, want %v", path, got, want)
		}
	}
}

// --- the denylist ------------------------------------------------------------

// TestDefaultDenyContentIsEmpty confirms paths.deny_content has no default —
// the operator names what to deny, or nothing is denied.
func TestDefaultDenyContentIsEmpty(t *testing.T) {
	var defaults struct {
		Paths struct {
			DenyContent []string `json:"deny_content"`
		} `json:"paths"`
	}
	if err := json.Unmarshal([]byte(config.Defaults), &defaults); err != nil {
		t.Fatal(err)
	}
	if len(defaults.Paths.DenyContent) != 0 {
		t.Fatalf("the defaults carry %d entries, want 0", len(defaults.Paths.DenyContent))
	}
}

// TestDenyPatternProducesTheExpectedERE pins each HCL construct — the ones
// the README documents as presets — to the pattern the sed-and-substitution
// produced for it, measured on 2026-08-22. These are no longer defaults but
// are still the documented HCL preset an operator copies in.
func TestDenyPatternProducesTheExpectedERE(t *testing.T) {
	for _, w := range []struct{ literal, pattern string }{
		{`data "external"`, `data[[:space:]]*[[:space:]]*"[[:space:]]*external[[:space:]]*"[[:space:]]*`},
		{`provisioner`, `provisioner`},
		{`local-exec`, `local-exec`},
		{`remote-exec`, `remote-exec`},
		{`templatefile(`, `templatefile[[:space:]]*\(`},
		{`filebase64(`, `filebase64[[:space:]]*\(`},
		{`file(`, `file[[:space:]]*\(`},
	} {
		if got := DenyPattern(w.literal); got != w.pattern {
			t.Errorf("DenyPattern(%q)\n got %s\nwant %s", w.literal, got, w.pattern)
		}
	}
}

func TestDenyPatternEscapesWhatSedEscaped(t *testing.T) {
	// Measured: the fourteen characters the sed class named, and what the
	// bash function made of a literal carrying all of them.
	literal := `a.b[c]{d}|e^f$g*h+i?j\\k`
	want := `a\.b\[c\]\{d\}\|e\^f\$g\*h\+i\?j\\\\k`
	if got := DenyPattern(literal); got != want {
		t.Errorf("DenyPattern(%q)\n got %s\nwant %s", literal, got, want)
	}
	if got := DenyPattern("jsondecode("); got != `jsondecode[[:space:]]*\(` {
		t.Errorf("DenyPattern(jsondecode() = %s", got)
	}
}

func TestDenyLabel(t *testing.T) {
	for literal, want := range map[string]string{
		"templatefile(":   "templatefile()",
		"file(":           "file()",
		`data "external"`: `data "external"`,
		"provisioner":     "provisioner",
	} {
		if got := DenyLabel(literal); got != want {
			t.Errorf("DenyLabel(%q) = %q, want %q", literal, got, want)
		}
	}
}

// hclDenylist is the HCL denylist the README documents as a preset. The
// defaults no longer carry it, but these tests pin its pattern behaviour.
var hclDenylist = []string{
	`data "external"`,
	"provisioner",
	"local-exec",
	"remote-exec",
	"templatefile(",
	"filebase64(",
	"file(",
}

func hclPolicy(t *testing.T) *Policy {
	t.Helper()
	p, err := NewPolicy([]string{"*"}, hclDenylist)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDenylistFirstMatchInConfigOrder(t *testing.T) {
	p := hclPolicy(t)
	cases := []struct {
		content string
		want    string
		hit     bool
	}{
		{"locals {\n  a = templatefile(\"x.tpl\", {})\n}\n", "templatefile()", true},
		{"locals {\n  a = templatefile (\"x.tpl\", {})\n}\n", "templatefile()", true},
		{"locals {\n  a = filebase64(\"x\")\n}\n", "filebase64()", true},
		{"output \"leak\" {\n  value = file(\"/etc/hosts\")\n}\n", "file()", true},
		{"data \"external\" \"d\" {\n  program = [\"sh\"]\n}\n", `data "external"`, true},
		{"data  \"external\"  \"d\" {\n}\n", `data "external"`, true},
		{"data\t\"external\"\n", `data "external"`, true},
		{"resource \"x\" \"y\" {\n  provisioner \"local-exec\" {}\n}\n", "provisioner", true},
		{"locals {\n  a = 1\n}\n", "", false},
		{"", "", false},
		// A file() call on the same line as a data "external" block: the
		// earlier entry wins, whichever comes first in the file.
		{"x = file(\"a\")\ndata \"external\" \"d\" {}\n", `data "external"`, true},
	}
	for _, c := range cases {
		got, hit := p.DenylistHit([]byte(c.content))
		if hit != c.hit || got != c.want {
			t.Errorf("DenylistHit(%q) = (%q, %v), want (%q, %v)", c.content, got, hit, c.want, c.hit)
		}
	}
}

// TestDenylistMatchesPerLine is the measured difference between `grep -E`
// and a regexp over a whole file: `[[:space:]]*` cannot cross a newline in
// grep, and can in Go. `data` on one line and `"external"` on the next is not
// a block header in HCL, and the guard never refused it; the port must not
// start to.
func TestDenylistMatchesPerLine(t *testing.T) {
	p := hclPolicy(t)
	for _, content := range []string{
		"data\n\"external\"\n",
		"templatefile\n(\"x\")\n",
		"file\n(\n",
	} {
		if got, hit := p.DenylistHit([]byte(content)); hit {
			t.Errorf("DenylistHit(%q) = %q; grep matched per line and found nothing", content, got)
		}
	}
}

func TestConfiguredDenylistReplaces(t *testing.T) {
	p, err := NewPolicy([]string{"*"}, []string{"jsondecode("})
	if err != nil {
		t.Fatal(err)
	}
	if got, hit := p.DenylistHit([]byte("a = jsondecode(\"{}\")\n")); !hit || got != "jsondecode()" {
		t.Errorf("got (%q, %v)", got, hit)
	}
	if got, hit := p.DenylistHit([]byte("a = file(\"x\")\n")); hit {
		t.Errorf("file( is not in a configured denylist that replaced the default, got %q", got)
	}
}

func TestDenyPatternMatchesItsOwnLiteral(t *testing.T) {
	// Any literal, once translated, matches a line that contains it
	// verbatim: escaping never loses a character.
	check(t, func(s string) bool {
		if s == "" || strings.ContainsRune(s, '\n') {
			return true
		}
		p, err := NewPolicy([]string{"*"}, []string{s})
		if err != nil {
			return false
		}
		_, hit := p.DenylistHit([]byte("prefix " + s + " suffix\n"))
		return hit
	})
}

// --- git status ----------------------------------------------------------------

func TestParseStatus(t *testing.T) {
	z := []byte(" M records-example-tech.tf\x00?? dns/new record.tf\x00 D gone.tf\x00A  .github/x.yml\x00")
	changed, refused := ParseStatus(z)
	if refused != nil {
		t.Fatalf("refused %+v", refused)
	}
	want := []string{"records-example-tech.tf", "dns/new record.tf", "gone.tf", ".github/x.yml"}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("changed = %q, want %q", changed, want)
	}

	if changed, refused := ParseStatus(nil); len(changed) != 0 || refused != nil {
		t.Errorf("an empty listing: %q %+v", changed, refused)
	}
}

func TestParseStatusRefusesARenameOrCopy(t *testing.T) {
	// Measured on git 2.50: a staged rename is two NUL-terminated fields,
	// the status-prefixed new path and then the bare old path.
	z := []byte(" M a.tf\x00R  new.tf\x00old.tf\x00?? b.tf\x00")
	changed, refused := ParseStatus(z)
	if refused == nil {
		t.Fatal("a rename was parsed")
	}
	if refused.Code != "R " || refused.Path != "new.tf" {
		t.Errorf("refused = %+v, want code 'R ' for new.tf", refused)
	}
	if changed != nil {
		t.Errorf("a refusal still returned paths: %q", changed)
	}
	if _, refused := ParseStatus([]byte("C  copy.tf\x00orig.tf\x00")); refused == nil || refused.Code != "C " {
		t.Errorf("a copy was not refused: %+v", refused)
	}
	// An unstaged rename is a delete and an untracked file, and goes through.
	if changed, refused := ParseStatus([]byte(" D old.tf\x00?? new.tf\x00")); refused != nil || len(changed) != 2 {
		t.Errorf("an unstaged rename: %q %+v", changed, refused)
	}
}

// --- the message ---------------------------------------------------------------

// TestSubjectAndBody pins the sed extraction, measured with both BSD and GNU
// sed on 2026-08-22: `sed -n 1p` for the subject, `sed 1d | sed '/./,$!d'`
// for the body, and the subject standing in for an empty body.
func TestSubjectAndBody(t *testing.T) {
	cases := []struct{ message, subject, body string }{
		{"Add the thing\n\nBecause the requester asked for the thing.\n",
			"Add the thing\n", "Because the requester asked for the thing.\n"},
		{"Just a subject\n", "Just a subject\n", "Just a subject\n"},
		{"Just a subject", "Just a subject", "Just a subject"},
		{"Subj\n\n\nBody line\nmore", "Subj\n", "Body line\nmore"},
		{"\nLeading blank\n\nbody\n", "\n", "Leading blank\n\nbody\n"},
		{"S\n\n\n", "S\n", "S\n"},
		{"S\nB\n", "S\n", "B\n"},
		{"S\n\nB\n\n\n", "S\n", "B\n\n\n"},
		{"S\n \nB\n", "S\n", " \nB\n"},
	}
	for _, c := range cases {
		if got := string(Subject([]byte(c.message))); got != c.subject {
			t.Errorf("Subject(%q) = %q, want %q", c.message, got, c.subject)
		}
		if got := string(Body([]byte(c.message))); got != c.body {
			t.Errorf("Body(%q) = %q, want %q", c.message, got, c.body)
		}
	}
}

func TestBodyIsNeverEmptyAndSubjectIsOneLine(t *testing.T) {
	check(t, func(message []byte) bool {
		if len(message) == 0 {
			return true
		}
		subject := Subject(message)
		body := Body(message)
		oneLine := bytes.Count(subject, []byte{'\n'}) <= 1 && bytes.HasPrefix(message, subject)
		return oneLine && len(body) > 0 && (bytes.HasSuffix(message, body) || bytes.Equal(body, subject))
	})
}

// --- what each refusal says ----------------------------------------------------

func TestReasonsKeepGiveUpsLineStructure(t *testing.T) {
	got := ReasonDeniedPaths([]string{"*.tf", "docs/*.md"}, []string{"AGENTS.md", ".github/workflows/x.yml"})
	want := "The agent changed files it is not allowed to change, so nothing\n" +
		"was committed. Only these paths may be changed in response to\n" +
		"an issue: *.tf docs/*.md. Refused paths:\n" +
		"  AGENTS.md\n" +
		"  .github/workflows/x.yml\n"
	if got != want {
		t.Errorf("ReasonDeniedPaths:\n%q\nwant\n%q", got, want)
	}

	got = ReasonSecret([]string{".falconet/needs-info.md", StagedLabelForTest})
	if !strings.Contains(got, "Where it matched:\n  .falconet/needs-info.md\n  "+StagedLabelForTest+"\n(commit-msg.txt") {
		t.Errorf("ReasonSecret does not indent the channels under the heading:\n%s", got)
	}
	for _, r := range []string{
		ReasonRename("R ", "new.tf"),
		ReasonDeniedContent([]string{"a.tf: file()"}),
		ReasonUnchanged(),
		ReasonNoMessage(".falconet/commit-msg.txt", []string{"a.tf"}),
		ReasonEmptyStaged([]string{"a.tf"}),
	} {
		if !strings.HasSuffix(r, "\n") || strings.HasSuffix(r, "\n\n") {
			t.Errorf("a reason must end in exactly one newline: %q", r)
		}
	}
	if r := ReasonRename("R ", "new.tf"); !strings.Contains(r, "status code 'R ' for new.tf)") {
		t.Errorf("ReasonRename: %q", r)
	}
}

// StagedLabelForTest mirrors scan.StagedLabel without importing the package:
// the reason only ever sees channel names as strings.
const StagedLabelForTest = "the staged change (git diff --cached)"

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}
