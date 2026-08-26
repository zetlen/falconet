package main

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	charterPath  = "docs/charter.md"
	registerPath = "docs/decisions.md"
	adrDir       = "docs/adr"
)

// The fields an ADR header may carry. The set is closed on purpose: a
// misspelled **Supercedes:** would otherwise be a field nothing reads and
// nothing refuses, which is how a cross-reference goes quietly missing.
var knownFields = map[string]bool{
	"Status":      true,
	"Serves":      true,
	"Reopen when": true,
	"Supersedes":  true,
	"Amends":      true,
	"Builds on":   true,
	"Reaffirms":   true,
}

// Serves and Reopen when are what this tool exists for; Status is what tells
// it which records are live.
var requiredFields = []string{"Status", "Serves", "Reopen when"}

// Files linked from anywhere in these are checked to exist. The set is the
// prose an adopter or an agent is sent to.
var linkChecked = []string{"README.md", "AGENTS.md"}

var (
	reADRFile      = regexp.MustCompile(`^(\d{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)
	reADRTitle     = regexp.MustCompile(`^# ADR-(\d{4}) — \S`)
	reField        = regexp.MustCompile(`^\*\*([A-Z][A-Za-z ]*?):\*\*[ ]?(.*)$`)
	reStatus       = regexp.MustCompile(`^(?:Accepted|Proposed|Superseded by \[ADR-(\d{4})\]\([^)]*\)) · \d{4}-\d{2}-\d{2}`)
	reInvariantRef = regexp.MustCompile(`\bI([1-9][0-9]*)\b`)
	reInvariantHed = regexp.MustCompile(`^### (I[1-9][0-9]*) · (\S.*)$`)
	reLink         = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	reInlineCode   = regexp.MustCompile("`[^`]*`")
	reADRMention   = regexp.MustCompile(`\bADR-(\d{4})\b`)
	reFence        = regexp.MustCompile("^\\s{0,3}(```|~~~)")
)

type finding struct {
	file string
	line int // 0 when the file as a whole is the subject
	msg  string
}

func (f finding) String() string {
	if f.line == 0 {
		return fmt.Sprintf("%s: %s", f.file, f.msg)
	}
	return fmt.Sprintf("%s:%d: %s", f.file, f.line, f.msg)
}

type field struct {
	value string
	line  int
}

type adr struct {
	path      string // repo-relative
	base      string // file name
	num       string // "0003"
	fields    map[string]field
	supersede string // the ADR number this one's Status says superseded it
}

// lint answers for the whole corpus at once, because most of what can go
// wrong here is a disagreement between two files rather than a fault in one.
func lint(fsys fs.FS) ([]finding, error) {
	var out []finding

	invariants, fs1, err := readCharter(fsys)
	if err != nil {
		return nil, err
	}
	out = append(out, fs1...)

	adrs, fs2, err := readADRs(fsys, invariants)
	if err != nil {
		return nil, err
	}
	out = append(out, fs2...)

	cited, fs3, err := readRegister(fsys, adrs, invariants)
	if err != nil {
		return nil, err
	}
	out = append(out, fs3...)

	out = append(out, checkSupersession(adrs)...)

	// An invariant nothing serves is either missing its record or is not an
	// invariant. Citations come from both directions: an ADR's Serves field
	// and a register row's.
	for _, id := range sortedKeys(invariants) {
		if !cited[id] {
			out = append(out, finding{charterPath, invariants[id], id + " is cited by no record: no ADR serves it and no decision in the register names it"})
		}
	}

	// Links last, over everything an adopter or an agent is sent to.
	files, err := prose(fsys)
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		fs4, err := checkLinks(fsys, p)
		if err != nil {
			return nil, err
		}
		out = append(out, fs4...)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out, nil
}

// readCharter returns the invariant ids the charter defines, mapped to the
// line each is declared on.
func readCharter(fsys fs.FS) (map[string]int, []finding, error) {
	lines, err := readLines(fsys, charterPath)
	if err != nil {
		return map[string]int{}, []finding{{charterPath, 0, "missing: this is the document every ADR's Serves field points at"}}, nil
	}
	var out []finding
	invariants := map[string]int{}
	seenSection := false
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") && strings.Contains(strings.ToLower(l), "invariant") {
			seenSection = true
		}
		m := reInvariantHed.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if prev, dup := invariants[m[1]]; dup {
			out = append(out, finding{charterPath, i + 1, fmt.Sprintf("%s is declared twice (also on line %d); an invariant id is an address and two records would disagree about where it points", m[1], prev)})
			continue
		}
		invariants[m[1]] = i + 1
	}
	if !seenSection {
		out = append(out, finding{charterPath, 0, "no `## The invariants` section"})
	}
	if len(invariants) == 0 {
		out = append(out, finding{charterPath, 0, "declares no invariants: each is a `### I<n> · <name>` heading"})
	}
	return invariants, out, nil
}

func readADRs(fsys fs.FS, invariants map[string]int) ([]*adr, []finding, error) {
	entries, err := fs.ReadDir(fsys, adrDir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", adrDir, err)
	}
	var out []finding
	var adrs []*adr
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := path.Join(adrDir, e.Name())
		m := reADRFile.FindStringSubmatch(e.Name())
		if m == nil {
			out = append(out, finding{p, 0, "name is not `NNNN-lower-case-words.md`, so it has no number the other records can cite"})
			continue
		}
		a, fs1, err := readADR(fsys, p, m[1], invariants)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, fs1...)
		if a != nil {
			adrs = append(adrs, a)
		}
	}
	if len(adrs) == 0 {
		out = append(out, finding{adrDir, 0, "holds no records"})
	}
	return adrs, out, nil
}

func readADR(fsys fs.FS, p, num string, invariants map[string]int) (*adr, []finding, error) {
	lines, err := readLines(fsys, p)
	if err != nil {
		return nil, nil, err
	}
	var out []finding
	a := &adr{path: p, base: path.Base(p), num: num, fields: map[string]field{}}

	if len(lines) == 0 || !reADRTitle.MatchString(lines[0]) {
		out = append(out, finding{p, 1, "first line must be `# ADR-NNNN — Title`"})
	} else if got := reADRTitle.FindStringSubmatch(lines[0])[1]; got != num {
		out = append(out, finding{p, 1, fmt.Sprintf("titled ADR-%s in a file numbered %s; a citation would reach the wrong record", got, num)})
	}

	// The header block is everything above the first `## ` heading. Nothing
	// else is parsed as a field, so a `**Bold:**` line in the body is prose.
	end := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") {
			end = i
			break
		}
	}
	header := lines[:end]

	var current string
	for i, l := range header {
		if reFence.MatchString(l) {
			out = append(out, finding{p, i + 1, "fenced code in the header block; the header is fields and prose, and a fence here would make the fields unparseable"})
			break
		}
		m := reField.FindStringSubmatch(l)
		if m == nil {
			// A continuation of the field above, or free prose. Either way it
			// belongs to whatever field was last opened.
			if current != "" {
				f := a.fields[current]
				f.value = strings.TrimSpace(f.value + " " + strings.TrimSpace(l))
				a.fields[current] = f
			}
			continue
		}
		name := m[1]
		if !knownFields[name] {
			out = append(out, finding{p, i + 1, fmt.Sprintf("unknown header field %q; the fields are %s", name, strings.Join(sortedKeys(knownFields), ", "))})
			current = ""
			continue
		}
		if _, dup := a.fields[name]; dup {
			out = append(out, finding{p, i + 1, fmt.Sprintf("**%s:** appears twice", name)})
		}
		a.fields[name] = field{value: strings.TrimSpace(m[2]), line: i + 1}
		current = name
	}

	for _, name := range requiredFields {
		f, ok := a.fields[name]
		if !ok {
			out = append(out, finding{p, 1, missingFieldMessage(name)})
			continue
		}
		if strings.TrimSpace(f.value) == "" {
			out = append(out, finding{p, f.line, fmt.Sprintf("**%s:** is empty", name)})
		}
	}

	if f, ok := a.fields["Status"]; ok && strings.TrimSpace(f.value) != "" {
		m := reStatus.FindStringSubmatch(f.value)
		if m == nil {
			out = append(out, finding{p, f.line, "**Status:** must begin `Accepted · YYYY-MM-DD`, `Proposed · YYYY-MM-DD`, or `Superseded by [ADR-NNNN](file.md) · YYYY-MM-DD`"})
		} else if m[1] != "" {
			a.supersede = m[1]
		}
	}

	if f, ok := a.fields["Serves"]; ok {
		out = append(out, checkServes(p, f, a.fields["Serves"].value, invariants)...)
	}
	return a, out, nil
}

func missingFieldMessage(name string) string {
	switch name {
	case "Serves":
		return "no **Serves:** field. Name the charter invariant this record is in service of — `**Serves:** I6 (adoption stays inside one operator's reach)`. A record that serves no invariant is describing a preference, not a decision."
	case "Reopen when":
		return "no **Reopen when:** field. Name the observation that should retire this decision — written now, while you have no stake in defending it. `nothing reopens this` is an answer, if it is true and you say why."
	default:
		return "no **" + name + ":** field"
	}
}

func checkServes(p string, f field, value string, invariants map[string]int) []finding {
	refs := reInvariantRef.FindAllStringSubmatch(value, -1)
	if len(refs) == 0 {
		return []finding{{p, f.line, "**Serves:** names no invariant; cite at least one by id, like `I3`"}}
	}
	var out []finding
	for _, r := range refs {
		id := "I" + r[1]
		if _, ok := invariants[id]; !ok {
			out = append(out, finding{p, f.line, fmt.Sprintf("**Serves:** cites %s, which %s does not declare", id, charterPath)})
		}
	}
	return out
}

// checkSupersession refuses a supersession only one of the two records knows
// about. The superseding record must mention the superseded one somewhere in
// its own header — under Supersedes when the whole record goes, under Amends
// when only a mechanism does.
func checkSupersession(adrs []*adr) []finding {
	byNum := map[string]*adr{}
	for _, a := range adrs {
		byNum[a.num] = a
	}
	var out []finding
	for _, a := range adrs {
		if a.supersede == "" {
			continue
		}
		other, ok := byNum[a.supersede]
		if !ok {
			out = append(out, finding{a.path, a.fields["Status"].line, fmt.Sprintf("**Status:** says ADR-%s superseded it, and there is no such record here", a.supersede)})
			continue
		}
		if !mentionsADR(other, a.num) {
			out = append(out, finding{other.path, 1, fmt.Sprintf("ADR-%s says this record superseded it, and this record's header does not mention ADR-%s. A supersession only one side knows about is how the corpus starts contradicting itself.", a.num, a.num)})
		}
	}
	return out
}

func mentionsADR(a *adr, num string) bool {
	for _, name := range []string{"Supersedes", "Amends", "Builds on", "Reaffirms", "Status"} {
		f, ok := a.fields[name]
		if !ok {
			continue
		}
		for _, m := range reADRMention.FindAllStringSubmatch(f.value, -1) {
			if m[1] == num {
				return true
			}
		}
	}
	return false
}

// readRegister holds the register's shape and returns the set of invariant
// ids its rows cite.
func readRegister(fsys fs.FS, adrs []*adr, invariants map[string]int) (map[string]bool, []finding, error) {
	cited := map[string]bool{}
	for _, a := range adrs {
		for _, m := range reInvariantRef.FindAllStringSubmatch(a.fields["Serves"].value, -1) {
			cited["I"+m[1]] = true
		}
	}

	lines, err := readLines(fsys, registerPath)
	if err != nil {
		return cited, []finding{{registerPath, 0, "missing: this is the index of every live decision"}}, nil
	}

	var out []finding
	want := []string{"Decision", "Serves", "Reopen when", "Record"}
	inTable, sawTable := false, false
	recorded := map[string]bool{}
	code := fenced(lines)
	for i, l := range lines {
		if code[i] || !strings.HasPrefix(strings.TrimSpace(l), "|") {
			inTable = false
			continue
		}
		cells := tableRow(l)
		if !inTable {
			if !equalStrings(cells, want) {
				continue // some other table
			}
			inTable, sawTable = true, true
			continue
		}
		if isSeparatorRow(cells) {
			continue
		}
		if len(cells) != len(want) {
			out = append(out, finding{registerPath, i + 1, fmt.Sprintf("row has %d cells, and the table has %d: %s", len(cells), len(want), strings.Join(want, " | "))})
			continue
		}
		for j, c := range cells {
			if strings.TrimSpace(c) == "" {
				out = append(out, finding{registerPath, i + 1, fmt.Sprintf("the %s cell is empty", want[j])})
			}
		}
		out = append(out, checkServes(registerPath, field{line: i + 1}, cells[1], invariants)...)
		for _, m := range reInvariantRef.FindAllStringSubmatch(cells[1], -1) {
			cited["I"+m[1]] = true
		}
		links := reLink.FindAllStringSubmatch(cells[3], -1)
		if len(links) == 0 {
			out = append(out, finding{registerPath, i + 1, "the Record cell links nothing; a decision with no record is a decision nobody can reopen"})
		}
		for _, m := range links {
			recorded[path.Base(strings.SplitN(m[1], "#", 2)[0])] = true
		}
	}
	if !sawTable {
		out = append(out, finding{registerPath, 0, "no register table; its header row is `" + strings.Join(want, " | ") + "`"})
	}

	for _, a := range adrs {
		if a.supersede != "" || recorded[a.base] {
			continue
		}
		out = append(out, finding{registerPath, 0, fmt.Sprintf("ADR-%s is accepted and no row cites it, so the decision it holds is indexed nowhere", a.num)})
	}
	return cited, out, nil
}

// checkLinks refuses a relative link to a file that is not there. External
// links and bare anchors are left alone; a dangling one of those is a
// different job.
func checkLinks(fsys fs.FS, p string) ([]finding, error) {
	lines, err := readLines(fsys, p)
	if err != nil {
		return nil, err
	}
	var out []finding
	code := fenced(lines)
	for i, l := range lines {
		if code[i] {
			continue
		}
		for _, m := range reLink.FindAllStringSubmatch(reInlineCode.ReplaceAllString(l, ""), -1) {
			target := m[1]
			if strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			if target == "" {
				continue
			}
			rel := path.Clean(path.Join(path.Dir(p), target))
			if _, err := fs.Stat(fsys, rel); err != nil {
				out = append(out, finding{p, i + 1, fmt.Sprintf("links %s, and there is no %s in this tree", m[1], rel)})
			}
		}
	}
	return out, nil
}

// prose is every document this tool reads links out of: the two entry points
// and everything under docs/.
func prose(fsys fs.FS) ([]string, error) {
	out := append([]string{}, linkChecked...)
	err := fs.WalkDir(fsys, "docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	present := out[:0]
	for _, p := range out {
		if _, err := fs.Stat(fsys, p); err == nil {
			present = append(present, p)
		}
	}
	sort.Strings(present)
	return present, nil
}

func readLines(fsys fs.FS, p string) ([]string, error) {
	b, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), nil
}

// fenced marks the lines inside a fenced code block, the fence lines included.
func fenced(lines []string) []bool {
	out := make([]bool, len(lines))
	open := ""
	for i, l := range lines {
		m := reFence.FindStringSubmatch(l)
		switch {
		case open == "" && m != nil:
			open, out[i] = m[1], true
		case open != "" && m != nil && m[1] == open:
			open, out[i] = "", true
		case open != "":
			out[i] = true
		}
	}
	return out
}

func tableRow(l string) []string {
	l = strings.TrimSpace(l)
	l = strings.TrimPrefix(l, "|")
	l = strings.TrimSuffix(l, "|")
	cells := strings.Split(l, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

func isSeparatorRow(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
