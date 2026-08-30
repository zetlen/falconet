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
	principlesPath = "README.md"
	registerPath   = "docs/decisions.md"
)

// Files linked from anywhere in these are checked to exist. The set is the
// prose an adopter or an agent is sent to; everything under docs/ joins it.
var linkChecked = []string{"README.md", "AGENTS.md"}

var (
	reInvariantRef = regexp.MustCompile(`\bI([1-9][0-9]*)\b`)
	rePrincipleHed = regexp.MustCompile(`^### (\S.*)$`)
	rePrincipleLed = regexp.MustCompile(`^\*\*([1-9][0-9]*)\.\*\* `)
	reLink         = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	reInlineCode   = regexp.MustCompile("`[^`]*`")
	reHeading      = regexp.MustCompile(`^#{1,6} +(\S.*)$`)
	reNotSlug      = regexp.MustCompile(`[^a-z0-9 -]`)
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
	line int
}

// lint answers for the whole corpus at once, because most of what can go
// wrong here is a disagreement between two files rather than a fault in one.
func lint(fsys fs.FS) ([]finding, error) {
	var out []finding

	invariants, fs1, err := readPrinciples(fsys)
	if err != nil {
		return nil, err
	}
	out = append(out, fs1...)

	fs2, err := readRegister(fsys, invariants)
	if err != nil {
		return nil, err
	}
	out = append(out, fs2...)

	// Links last, over everything an adopter or an agent is sent to.
	files, err := prose(fsys)
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		fs3, err := checkLinks(fsys, p)
		if err != nil {
			return nil, err
		}
		out = append(out, fs3...)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out, nil
}

// readPrinciples returns the ids of the principles the README declares,
// mapped to the line each is declared on. A principle is a `### ` heading
// under the `## The invariant principles` section; its id is its position —
// `I1` is the first — and the paragraph under the heading opens with the
// same number as `**n.**`, so the address a register row cites is also the
// number a reader sees. A lead that disagrees with its position is two
// addresses for one principle, and two decisions would disagree about where
// it points.
func readPrinciples(fsys fs.FS) (map[string]int, []finding, error) {
	lines, err := readLines(fsys, principlesPath)
	if err != nil {
		return map[string]int{}, []finding{{principlesPath, 0, "missing: this is the document every decision's Serves cell points at"}}, nil
	}
	var out []finding
	invariants := map[string]int{}
	code := fenced(lines)
	inSection, seenSection, n := false, false, 0
	for i, l := range lines {
		if code[i] {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			lower := strings.ToLower(l)
			inSection = strings.Contains(lower, "principle") || strings.Contains(lower, "invariant")
			if inSection {
				seenSection = true
			}
			continue
		}
		if !inSection {
			continue
		}
		m := rePrincipleHed.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n++
		id := fmt.Sprintf("I%d", n)
		invariants[id] = i + 1
		lead := ""
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) != "" {
				lead = lines[j]
				break
			}
		}
		switch lm := rePrincipleLed.FindStringSubmatch(lead); {
		case lm == nil:
			out = append(out, finding{principlesPath, i + 1, fmt.Sprintf("%s (%s) has no `**%d.**` lead on the first line under its heading; that number is the address a Serves cell cites", id, m[1], n)})
		case lm[1] != fmt.Sprint(n):
			out = append(out, finding{principlesPath, i + 1, fmt.Sprintf("%s (%s) is principle %d by position but its lead says **%s.**; an address that disagrees with its position points two decisions at different things", id, m[1], n, lm[1])})
		}
	}
	if !seenSection {
		out = append(out, finding{principlesPath, 0, "no `## The invariant principles` section"})
	}
	if len(invariants) == 0 {
		out = append(out, finding{principlesPath, 0, "declares no principles: each is a `### <name>` heading under that section, led by `**<n>.**`"})
	}
	return invariants, out, nil
}

// readRegister reads the table of live decisions and the sections beneath it.
// Every row names a principle the README declares and links the section
// that records it; the section must exist, in this file, because the
// register is the one document a decision is read from.
func readRegister(fsys fs.FS, invariants map[string]int) ([]finding, error) {
	lines, err := readLines(fsys, registerPath)
	if err != nil {
		return []finding{{registerPath, 0, "missing: this is the index of every live decision"}}, nil
	}

	var out []finding
	code := fenced(lines)
	anchors := map[string]bool{}
	for i, l := range lines {
		if code[i] {
			continue
		}
		if m := reHeading.FindStringSubmatch(l); m != nil {
			anchors[slug(m[1])] = true
		}
	}

	want := []string{"Decision", "Serves", "Reopen when", "Record"}
	inTable, sawTable := false, false
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
		links := reLink.FindAllStringSubmatch(cells[3], -1)
		if len(links) == 0 {
			out = append(out, finding{registerPath, i + 1, "the Record cell links nothing; a decision with no record is a decision nobody can reopen"})
		}
		for _, m := range links {
			target := m[1]
			if !strings.HasPrefix(target, "#") {
				continue // a file; checkLinks holds it
			}
			if !anchors[strings.TrimPrefix(target, "#")] {
				out = append(out, finding{registerPath, i + 1, fmt.Sprintf("the Record cell links %s, and no heading in this file has that anchor", target)})
			}
		}
	}
	if !sawTable {
		out = append(out, finding{registerPath, 0, "no register table; its header row is `" + strings.Join(want, " | ") + "`"})
	}
	return out, nil
}

// slug is the anchor GitHub gives a heading: lower-cased, punctuation
// dropped, spaces to hyphens. Close enough for the headings this repository
// writes; a heading that needs more than this is a heading to simplify.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = reNotSlug.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}

func checkServes(p string, f field, value string, invariants map[string]int) []finding {
	// `retired` is a row whose only principle was retired with the mechanism
	// it served: the README says the mechanism goes, and the row goes with
	// it. It is accepted so the register can describe the tree as it is
	// while that removal is in progress, and it is not an address.
	if strings.TrimSpace(value) == "retired" {
		return nil
	}
	refs := reInvariantRef.FindAllStringSubmatch(value, -1)
	if len(refs) == 0 {
		return []finding{{p, f.line, "the Serves cell names no principle; cite at least one by id, like `I3`, or say `retired`"}}
	}
	var out []finding
	for _, m := range refs {
		id := "I" + m[1]
		if _, ok := invariants[id]; !ok {
			out = append(out, finding{p, f.line, fmt.Sprintf("Serves cites %s, which %s does not declare", id, principlesPath)})
		}
	}
	return out
}

// checkLinks refuses a relative link to a file that is not there. External
// links and bare anchors are left alone; a dangling one of those is a
// different job — except in the register, where readRegister holds them.
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
// and everything under docs/, history included.
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
