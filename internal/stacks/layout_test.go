package stacks

import (
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"testing/quick"
)

// tf is one file's worth of OpenTofu, so a fixture reads as a tree rather
// than as a wall of MapFile literals.
func tf(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }

// repo is the shape #23 happened in and then some: three declared stacks, a
// fourth nobody declared, a module two of them share, a module of that
// module, a directory tofu's cache put .tf files in, and a .tf at the root.
var repo = fstest.MapFS{
	"main.tf":                    tf("terraform {}\n"),
	"dns/main.tf":                tf("module \"records\" {\n  source = \"../modules/records\"\n}\n"),
	"workspace/main.tf":          tf("locals { a = 1 }\n"),
	"site/main.tf":               tf("locals { a = 1 }\n"),
	"talaria-gcp/variables.tf":   tf("variable \"tier\" {}\n"),
	"talaria-gcp/main.tf":        tf("module \"r\" {\n  source = \"../modules/records\"\n}\n"),
	"modules/records/main.tf":    tf("module \"inner\" {\n  source = \"./inner\"\n}\n"),
	"modules/records/inner/i.tf": tf("locals { a = 1 }\n"),
	"modules/records/tpl.tftpl":  tf("nothing\n"),
	".terraform/cache/x.tf":      tf("locals { a = 1 }\n"),
	"node_modules/p/y.tf":        tf("locals { a = 1 }\n"),
	"README.md":                  tf("# repo\n"),
}

func TestDiscover(t *testing.T) {
	got, err := Discover(repo)
	if err != nil {
		t.Fatal(err)
	}
	// The root is never a stack, the two skipped trees are never entered,
	// and modules/records and its inner module belong to whoever sources
	// them rather than standing as stacks of their own.
	want := []string{"dns", "site", "talaria-gcp", "workspace"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover = %q, want %q", got, want)
	}
}

// A module the ROOT sources is still a module, even though the root is not a
// stack: the scan for edges includes the root, and only the answer excludes
// it. Without that, a repository whose one .tf at the top wires up ./modules
// would have every module reported as a stack of its own.
func TestDiscoverModuleOfTheRoot(t *testing.T) {
	got, err := Discover(fstest.MapFS{
		"main.tf":            tf("module \"m\" {\n  source = \"./modules/vpc\"\n}\n"),
		"modules/vpc/vpc.tf": tf("locals { a = 1 }\n"),
		"prod/main.tf":       tf("locals { a = 1 }\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"prod"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Discover = %q, want %q", got, want)
	}
}

// A module NOTHING sources is a root module by every test available from the
// tree, and is reported as one. Guessing otherwise from its directory's name
// would be magic, and a repository that keeps an unused module can say so in
// stacks.validate_only.
func TestDiscoverOrphanModule(t *testing.T) {
	got, err := Discover(fstest.MapFS{"modules/vpc/vpc.tf": tf("locals { a = 1 }\n")})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"modules/vpc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Discover = %q, want %q", got, want)
	}
}

func TestSources(t *testing.T) {
	fsys := fstest.MapFS{
		"s/main.tf": tf(`
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}
module "a" {
  source = "./a"
}
module "b" {
  source  = "../shared/b"
}
module "registry" {
  source = "terraform-aws-modules/vpc/aws"
}
module "git" {
  source = "git::https://example.com/m.git"
}
`),
		"s/also.tf":  tf("module \"c\" {\n\tsource\t=\t\"./a\"\n}\n"),
		"s/notes.md": tf("source = \"./decoy\"\n"),
	}
	got, err := sources(fsys, "s")
	if err != nil {
		t.Fatal(err)
	}
	// Local paths only, resolved against the directory, deduplicated, and
	// sorted. A registry address, a git address and a provider's `source`
	// are all not paths in this tree.
	want := []string{"s/a", "shared/b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %q, want %q", got, want)
	}
}

// A source that climbs out of the repository names nothing here, and a
// coverage answer built on it would be about a tree this run cannot see.
func TestSourcesOutsideTheRepository(t *testing.T) {
	got, err := sources(fstest.MapFS{
		"s/main.tf": tf("module \"m\" {\n  source = \"../../elsewhere\"\n}\nmodule \"n\" {\n  source = \"..\"\n}\n"),
	}, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("sources = %q, want none", got)
	}
}

func TestUses(t *testing.T) {
	all := []string{"dns", "site", "talaria-gcp", "workspace"}
	got, err := Uses(repo, all)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		// Shared, so both; transitive, so the inner one too.
		"modules/records":       {"dns", "talaria-gcp"},
		"modules/records/inner": {"dns", "talaria-gcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Uses = %v, want %v", got, want)
	}
}

// A module that sources its way back to something already seen must not spin
// the walk forever. Terraform forbids the cycle; this holds the walk to it
// anyway, because a hang in CI is a run nobody gets an answer from.
func TestUsesCycle(t *testing.T) {
	got, err := Uses(fstest.MapFS{
		"s/main.tf":   tf("module \"a\" {\n  source = \"./a\"\n}\n"),
		"s/a/a.tf":    tf("module \"b\" {\n  source = \"../b\"\n}\n"),
		"s/b/b.tf":    tf("module \"a\" {\n  source = \"../a\"\n}\n"),
		"s/b/back.tf": tf("module \"s\" {\n  source = \"..\"\n}\n"),
	}, []string{"s"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"s/a": {"s"}, "s/b": {"s"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Uses = %v, want %v", got, want)
	}
}

func TestResolve(t *testing.T) {
	discovered := []string{"dns", "site"}
	cases := []struct {
		name        string
		plan, check []string
		want        Layout
	}{
		// Naming neither list is how a repository says "you can see the
		// shape of this as well as I can".
		{"neither list", nil, nil, Layout{Plan: []string{"dns", "site"}}},
		{"empty lists are neither", []string{}, []string{""}, Layout{Plan: []string{"dns", "site"}}},
		{"plan alone", []string{"dns"}, nil, Layout{Plan: []string{"dns"}, Declared: true}},
		// validate_only alone is how a repository says it plans NOTHING,
		// and it is declared: falconet stops guessing about directories.
		{"validate_only alone", nil, []string{"site"}, Layout{Check: []string{"site"}, Declared: true}},
		{"both", []string{"dns"}, []string{"site"}, Layout{Plan: []string{"dns"}, Check: []string{"site"}, Declared: true}},
	}
	for _, c := range cases {
		if got := Resolve(discovered, c.plan, c.check); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Resolve = %+v, want %+v", c.name, got, c.want)
		}
	}
	l := Layout{Plan: []string{"dns"}, Check: []string{"site"}}
	if want := []string{"dns", "site"}; !reflect.DeepEqual(l.All(), want) {
		t.Errorf("All = %q, want %q", l.All(), want)
	}
	if !l.Plans("dns") || l.Plans("site") || l.Plans("") {
		t.Error("Plans does not answer for the planned list alone")
	}
}

func TestOwner(t *testing.T) {
	all := []string{"dns", "dns/nested", "site"}
	for p, want := range map[string]string{
		"dns/main.tf":            "dns",
		"dns/nested/main.tf":     "dns/nested", // the longest match, not the first
		"dns":                    "dns",
		"dnsx/main.tf":           "", // a prefix of the NAME is not a path inside it
		"dns-old/main.tf":        "",
		"site/deep/a/b/c.tf":     "site",
		"main.tf":                "",
		"modules/records/mod.tf": "",
	} {
		if got := Owner(p, all); got != want {
			t.Errorf("Owner(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestReach(t *testing.T) {
	// talaria-gcp is deliberately NOT in the layout: it is the directory the
	// config never named, which is the whole of #23.
	all := []string{"dns", "workspace", "site"}
	uses := map[string][]string{
		"modules/records":       {"dns", "site"},
		"modules/records/inner": {"dns", "site"},
	}
	cases := []struct {
		name           string
		changed        []string
		touched, uncov []string
	}{
		{"a file in a stack reaches it",
			[]string{"dns/records-example.tf"}, []string{"dns"}, nil},
		// The whole of #23: the change landed in a directory the config did
		// not name, so it reaches nothing and the plan it would have been
		// given is a plan of somewhere else.
		{"a file in an undeclared directory reaches nothing",
			[]string{"talaria-gcp/variables.tf"}, nil, []string{"talaria-gcp/variables.tf"}},
		{"a file in a shared module reaches every stack that sources it",
			[]string{"modules/records/main.tf"}, []string{"dns", "site"}, nil},
		{"and through a module of that module",
			[]string{"modules/records/inner/i.tf"}, []string{"dns", "site"}, nil},
		// A module's templates and data files change what it plans as surely
		// as its .tf files do.
		{"a module's non-tf files reach it too",
			[]string{"modules/records/tpl.tftpl"}, []string{"dns", "site"}, nil},
		{"the touched stacks come back in the layout's order",
			[]string{"site/a.tf", "dns/b.tf"}, []string{"dns", "site"}, nil},
		{"a .tf at the repository root is in no stack: the root is never one",
			[]string{"main.tf"}, nil, []string{"main.tf"}},
		{"tfvars count as Terraform",
			[]string{"elsewhere/prod.tfvars", "elsewhere/x.tf.json"},
			nil, []string{"elsewhere/prod.tfvars", "elsewhere/x.tf.json"}},
		// A README outside every stack changes no plan, so it is not a
		// change nothing planned — it is a change nothing needed to plan.
		{"a file that is not Terraform, outside every stack, is nobody's business",
			[]string{"README.md", ".github/falconet.json"}, nil, nil},
		{"and one inside a stack still reaches it",
			[]string{"dns/README.md"}, []string{"dns"}, nil},
		{"a stack is named once however many of its files changed",
			[]string{"dns/a.tf", "dns/b.tf", "dns/c/d.tf"}, []string{"dns"}, nil},
		{"nothing changed reaches nothing", nil, nil, nil},
	}
	for _, c := range cases {
		touched, uncov := Reach(c.changed, all, uses)
		if !reflect.DeepEqual(touched, c.touched) || !reflect.DeepEqual(uncov, c.uncov) {
			t.Errorf("%s:\n got %q / %q\nwant %q / %q", c.name, touched, uncov, c.touched, c.uncov)
		}
	}
}

func TestIntersect(t *testing.T) {
	// The config's order survives, because that is the order the plans are
	// written to plan.txt in and a reviewer reads them in.
	if got := Intersect([]string{"dns", "site", "workspace"}, []string{"workspace", "dns"}); !reflect.DeepEqual(got, []string{"dns", "workspace"}) {
		t.Errorf("Intersect = %q", got)
	}
	if got := Intersect([]string{"dns"}, []string{"site"}); got != nil {
		t.Errorf("Intersect = %q, want none", got)
	}
}

func TestUndeclared(t *testing.T) {
	discovered := []string{"dns", "site", "talaria-gcp", "workspace"}
	declared := Layout{Plan: []string{"dns"}, Check: []string{"workspace", "site"}, Declared: true}
	if got := Undeclared(discovered, declared); !reflect.DeepEqual(got, []string{"talaria-gcp"}) {
		t.Errorf("Undeclared = %q, want [talaria-gcp]", got)
	}
	// A layout that names nothing leaves nothing out: everything discovery
	// found is in it.
	if got := Undeclared(discovered, Layout{Plan: discovered}); got != nil {
		t.Errorf("Undeclared of an undeclared layout = %q, want none", got)
	}
}

// For any set of stacks and any set of changed paths: every path is
// accounted for exactly once — it reaches a stack, or it is reported
// uncovered, or it is not Terraform — every touched stack really holds or is
// reached by one of the paths, and nothing is reported twice.
func TestReachAccountsForEveryPath(t *testing.T) {
	f := func(dirs []string, files []string) bool {
		var all []string
		for _, d := range dirs {
			d = strings.Trim(path.Clean("/"+d), "/")
			if d == "" || d == "." || strings.Contains(d, "\x00") {
				continue
			}
			all = append(all, d)
		}
		sort.Strings(all)
		var changed []string
		for _, p := range files {
			p = strings.Trim(path.Clean("/"+p), "/")
			if p == "" || p == "." {
				continue
			}
			changed = append(changed, p)
		}
		touched, uncovered := Reach(changed, all, nil)
		seen := map[string]bool{}
		for _, s := range touched {
			if seen[s] {
				return false // reported twice
			}
			seen[s] = true
			if !contains(all, s) {
				return false // a stack that is not in the layout
			}
		}
		for _, p := range uncovered {
			if !isTerraform(p) || Owner(p, all) != "" {
				return false // covered, or not Terraform, and still reported
			}
		}
		for _, p := range changed {
			if o := Owner(p, all); o != "" {
				if !seen[o] {
					return false // its stack was not reported
				}
				continue
			}
			if isTerraform(p) && !contains(uncovered, p) {
				return false // in no stack, Terraform, and not reported
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 2000}); err != nil {
		t.Error(err)
	}
}
