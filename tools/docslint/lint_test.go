package main

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// A corpus that agrees with itself: one invariant, one decision that serves
// it, one section that records it. Every case below is this, broken in one
// place, so a case names the break it exists for.
func corpus() fstest.MapFS {
	return fstest.MapFS{
		"docs/charter.md": &fstest.MapFile{Data: []byte(`# What falconet is for

## The invariants

### I1 · A person decides the apply

falconet plans; it never applies.
`)},
		"docs/decisions.md": &fstest.MapFile{Data: []byte(`# The decision register

| Decision | Serves | Reopen when | Record |
| --- | --- | --- | --- |
| It never applies | I1 | never | [never applies](#it-never-applies) |

## It never applies

Prose. See [operating](operating.md).
`)},
		"docs/operating.md":             &fstest.MapFile{Data: []byte("# Operating\n")},
		"docs/history/0002-a-record.md": &fstest.MapFile{Data: []byte("# ADR-0002 — A record\n\nHow it was reached.\n")},
		"README.md":                     &fstest.MapFile{Data: []byte("# falconet\n\nSee [the charter](docs/charter.md).\n")},
		"AGENTS.md":                     &fstest.MapFile{Data: []byte("# Working on falconet\n\nRead [the register](docs/decisions.md).\n")},
	}
}

func write(fsys fstest.MapFS, p, s string) fstest.MapFS {
	fsys[p] = &fstest.MapFile{Data: []byte(s)}
	return fsys
}

func sub(fsys fstest.MapFS, p, old, new string) fstest.MapFS {
	s := string(fsys[p].Data)
	if !strings.Contains(s, old) {
		panic("test fixture does not contain " + old)
	}
	return write(fsys, p, strings.Replace(s, old, new, 1))
}

func TestTheGoodCorpusIsSilent(t *testing.T) {
	found, err := lint(corpus())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("a corpus that agrees with itself produced findings: %v", found)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"It never applies":                          "it-never-applies",
		"The language is Go":                        "the-language-is-go",
		"Verbs never call each other; `.falconet/`": "verbs-never-call-each-other-falconet",
		"Never `-target`":                           "never--target",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEachBreakIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(fstest.MapFS) fstest.MapFS
		want   string
	}{
		{"Serves cites an invariant the charter does not declare", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "| I1 |", "| I9 |")
		}, "cites I9, which docs/charter.md does not declare"},

		{"Serves names no invariant at all", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "| I1 |", "| the reviewer |")
		}, "names no invariant"},

		{"an empty cell", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "| never |", "| |")
		}, "the Reopen when cell is empty"},

		{"a Record cell that links nothing", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "[never applies](#it-never-applies)", "below")
		}, "links nothing"},

		{"a Record anchor no heading answers to", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "(#it-never-applies)", "(#it-always-applies)")
		}, "no heading in this file has that anchor"},

		{"a Record link to a file that is not there", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "[never applies](#it-never-applies)", "[gone](adr/0001-gone.md)")
		}, "there is no docs/adr/0001-gone.md in this tree"},

		{"a link to a file that is not there, from history", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/history/0002-a-record.md", "How it was reached.", "See [ADR-0001](0001-gone.md).")
		}, "there is no docs/history/0001-gone.md in this tree"},

		{"an invariant no decision serves", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/charter.md", "### I1 · A person decides the apply",
				"### I2 · Nobody's business\n\nUnserved.\n\n### I1 · A person decides the apply")
		}, "I2 is cited by no decision"},

		{"one invariant id declared twice", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/charter.md", "### I1 · A person decides the apply",
				"### I1 · Something else\n\nProse.\n\n### I1 · A person decides the apply")
		}, "declared twice"},

		{"no charter", func(f fstest.MapFS) fstest.MapFS {
			delete(f, "docs/charter.md")
			return f
		}, "docs/charter.md: missing"},

		{"no register", func(f fstest.MapFS) fstest.MapFS {
			delete(f, "docs/decisions.md")
			return f
		}, "docs/decisions.md: missing"},

		{"a register with no table", func(f fstest.MapFS) fstest.MapFS {
			return write(f, "docs/decisions.md", "# The decision register\n\nNothing yet.\n")
		}, "no register table"},

		{"a heading inside a fence does not count as an anchor", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "## It never applies", "```\n## It never applies\n```")
		}, "no heading in this file has that anchor"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found, err := lint(tc.break_(corpus()))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range found {
				if strings.Contains(f.String(), tc.want) {
					return
				}
			}
			t.Fatalf("nothing refused it.\nwanted a finding containing: %s\ngot: %v", tc.want, found)
		})
	}
}

// The suite's own subject is this repository. Every other case above proves
// the tool can say no; this one is the tree saying the records agree.
func TestTheRecordsInThisRepository(t *testing.T) {
	found, err := lint(os.DirFS("../.."))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("%s", f)
	}
}
