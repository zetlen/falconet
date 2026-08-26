package main

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// A corpus that agrees with itself: one invariant, one record that serves it,
// one row that indexes the record. Every case below is this, broken in one
// place, so a case names the break it exists for.
func corpus() fstest.MapFS {
	return fstest.MapFS{
		"docs/charter.md": &fstest.MapFile{Data: []byte(`# What falconet is for

## The invariants

### I1 · A person decides the apply

falconet plans; it never applies.
`)},
		"docs/adr/0002-a-record.md": &fstest.MapFile{Data: []byte(`# ADR-0002 — A record

**Status:** Accepted · 2026-08-20
**Serves:** I1 (a person decides the apply)
**Reopen when:** somebody wants the tool to apply.

## Context

Prose.
`)},
		"docs/decisions.md": &fstest.MapFile{Data: []byte(`# The decision register

| Decision | Serves | Reopen when | Record |
| --- | --- | --- | --- |
| It never applies | I1 | never | [ADR-0002](adr/0002-a-record.md) |

Trailing prose.
`)},
		"README.md": &fstest.MapFile{Data: []byte("# falconet\n\nSee [the charter](docs/charter.md).\n")},
		"AGENTS.md": &fstest.MapFile{Data: []byte("# Working on falconet\n\nRead [the register](docs/decisions.md).\n")},
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

func TestEachBreakIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(fstest.MapFS) fstest.MapFS
		want   string
	}{
		{"no Serves field", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Serves:** I1 (a person decides the apply)\n", "")
		}, "no **Serves:** field"},

		{"no Reopen when field", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Reopen when:** somebody wants the tool to apply.\n", "")
		}, "no **Reopen when:** field"},

		{"empty Reopen when", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Reopen when:** somebody wants the tool to apply.", "**Reopen when:**")
		}, "**Reopen when:** is empty"},

		{"Serves cites an invariant the charter does not declare", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Serves:** I1", "**Serves:** I9")
		}, "cites I9, which docs/charter.md does not declare"},

		{"Serves names no invariant at all", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Serves:** I1 (a person decides the apply)", "**Serves:** the reviewer")
		}, "names no invariant"},

		{"a misspelled field name", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Status:**", "**Supercedes:** nothing\n**Status:**")
		}, `unknown header field "Supercedes"`},

		{"a Status with no shape", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Status:** Accepted · 2026-08-20", "**Status:** done I think")
		}, "**Status:** must begin"},

		{"a link to a file that is not there", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "## Context", "See [ADR-0001](0001-gone.md).\n\n## Context")
		}, "there is no docs/adr/0001-gone.md in this tree"},

		{"an accepted record no row indexes", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "[ADR-0002](adr/0002-a-record.md)", "[operating](operating.md)")
		}, "ADR-0002 is accepted and no row cites it"},

		{"a Record cell that links nothing", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/decisions.md", "[ADR-0002](adr/0002-a-record.md)", "ADR-0002")
		}, "links nothing"},

		{"a supersession only one side knows about", func(f fstest.MapFS) fstest.MapFS {
			f = write(f, "docs/adr/0003-the-other.md", `# ADR-0003 — The other

**Status:** Accepted · 2026-08-21
**Serves:** I1
**Reopen when:** never.

## Context
`)
			f = sub(f, "docs/adr/0002-a-record.md", "**Status:** Accepted · 2026-08-20",
				"**Status:** Superseded by [ADR-0003](0003-the-other.md) · 2026-08-21")
			return sub(f, "docs/decisions.md", "| It never applies", "| The other | I1 | never | [ADR-0003](adr/0003-the-other.md) |\n| It never applies")
		}, "does not mention ADR-0002"},

		{"a status naming a superseding record that is not here", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Status:** Accepted · 2026-08-20",
				"**Status:** Superseded by [ADR-0044](0044-nowhere.md) · 2026-08-21")
		}, "there is no such record here"},

		{"an invariant no record serves", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/charter.md", "### I1 · A person decides the apply",
				"### I2 · Nobody's business\n\nUnserved.\n\n### I1 · A person decides the apply")
		}, "I2 is cited by no record"},

		{"one invariant id declared twice", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/charter.md", "### I1 · A person decides the apply",
				"### I1 · Something else\n\nProse.\n\n### I1 · A person decides the apply")
		}, "declared twice"},

		{"a file numbered one thing and titled another", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "# ADR-0002 — A record", "# ADR-0009 — A record")
		}, "titled ADR-0009 in a file numbered 0002"},

		{"a file name with no number", func(f fstest.MapFS) fstest.MapFS {
			return write(f, "docs/adr/thoughts.md", "# Thoughts\n")
		}, "has no number the other records can cite"},

		{"a fence in the header block", func(f fstest.MapFS) fstest.MapFS {
			return sub(f, "docs/adr/0002-a-record.md", "**Status:**", "```\nnot here\n```\n**Status:**")
		}, "fenced code in the header block"},

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
