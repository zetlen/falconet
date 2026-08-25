package stacks

// The repository's layout: which directories hold OpenTofu, which of them a
// change reaches, and which of them this repository plans.
//
// # Why this exists (#23)
//
// v0.2.0 read `stacks.plan` as the enumeration of everything falconet knew
// about and planned all of it, whatever the change was. A consumer added a
// stack, did not add it to `.github/falconet.json`, and filed a request whose
// fix lived in it: the path guard passed (`*.tf` matches a `.tf` anywhere),
// every configured stack validated and planned clean by construction — the
// diff was nowhere near them — and the pull request carried, as its entire
// plan, "No changes. Your infrastructure matches the configuration." under a
// diff that changed a database tier. A true plan of a stack the change does
// not touch is the most expensive kind of wrong answer: it reads as evidence.
//
// So the plan follows the diff. What a change reaches is what gets planned,
// and a change that reaches nothing this repository plans gets no pull
// request at all — it gets a person.
//
// # The assumptions, and why they are the safe ones
//
// This is the ordinary shape of an OpenTofu repository, and nothing here is
// clever about it:
//
//   - A directory that directly holds `.tf` files is a root module — a stack
//     — unless some other directory names it as a local module `source`, in
//     which case it is a module and belongs to whatever sources it.
//   - The repository root is never a stack. falconet runs `tofu -chdir=<dir>`
//     and never plans the tree it is standing in.
//   - A change inside a stack reaches that stack. A change inside a module
//     reaches every stack that sources it, transitively.
//
// Every one of those is what Atlantis, Terragrunt and Spacelift assume, and
// a repository that violates one is a repository whose operator can say so in
// `stacks`. What this deliberately does NOT do is narrow the plan itself:
// there is no `-target`, ever. A plan is of a whole stack or it is not a
// plan, and an operator who reads `Note: resource targeting is in effect` in
// their run log is right to wonder what else this tool decided on its own.

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// skipped are the directories discovery never enters: git's own, tofu's
// provider cache, and a JavaScript dependency tree that can hold anything —
// besides every dot-directory, which is somebody's tool's and not a stack.
var skipped = map[string]bool{".git": true, ".terraform": true, "node_modules": true}

// localSource matches a `source = "./x"` or `source = "../x"` argument in a
// `module` block. The quoted-path form is all Terraform's own language allows
// for a local module, and the leading dot is what tells one from a registry
// or git address — `source = "hashicorp/aws"` inside required_providers is
// matched by the expression and dropped by the dot test, which is why the
// test is here rather than in a more elaborate pattern. `.tf.json` is not
// read: a repository that writes its modules in JSON gets discovery of its
// directories and no dependency edges, which is a smaller wrong answer than
// a half-parsed one.
var localSource = regexp.MustCompile(`(?m)^[ \t]*source[ \t]*=[ \t]*"(\.[^"]*)"`)

// terraformFile is the suffix set that can change a plan. A changed file that
// is not one of these and lies outside every stack is not falconet's business
// — a README at the root is not an unplanned change — and Uncovered ignores
// it.
var terraformFile = []string{".tf", ".tf.json", ".tfvars", ".tfvars.json"}

// Discover is every root module in the tree: a sorted list of root-relative
// directories that directly hold a `.tf` file, minus the ones some other such
// directory sources as a local module. The repository root itself is never
// one (see the header).
//
// This is what `init` writes into the config and what a run falls back on
// when the config names no stacks at all, so the two answers cannot drift:
// discovery is one function.
func Discover(fsys fs.FS) ([]string, error) {
	dirs, err := tfDirs(fsys)
	if err != nil {
		return nil, err
	}
	// A directory that something else sources is a module, not a stack. The
	// scan includes the repository root, which holds no stack but may well
	// hold the `.tf` that sources one.
	isModule := map[string]bool{}
	for _, d := range append(dirs, ".") {
		targets, err := sources(fsys, d)
		if err != nil {
			return nil, err
		}
		for _, t := range targets {
			isModule[t] = true
		}
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if !isModule[d] {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

// tfDirs is every directory holding a `.tf` file directly, root excluded.
func tfDirs(fsys fs.FS) ([]string, error) {
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
	dirs := make([]string, 0, len(found))
	for d := range found {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// sources is the local module directories one directory's `.tf` files name,
// resolved against it and cleaned, sorted and without duplicates. A source
// that climbs out of the repository — `../../elsewhere` from a top-level
// stack — is dropped: it is not a path in this tree and nothing here can
// reach it.
func sources(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		// A directory that cannot be read holds no edges anyone can act on.
		// Discovery already found what it found by walking; refusing the
		// whole run over one unreadable directory would be a worse answer
		// than a missing edge.
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, m := range localSource.FindAllSubmatch(raw, -1) {
			target := path.Join(dir, string(m[1]))
			if target == "." || target == ".." || strings.HasPrefix(target, "../") {
				continue
			}
			if !seen[target] {
				seen[target] = true
				out = append(out, target)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Layout is what this repository does with each directory that holds
// OpenTofu: the stacks it plans, the stacks it only validates, and whether
// the config said so or discovery did.
type Layout struct {
	// Plan is the stacks a human's approval can turn into an apply, in the
	// order they were named.
	Plan []string
	// Check is the stacks that are validated and never planned.
	Check []string
	// Declared is whether `stacks` in the config named any of this. When it
	// did, the config is authoritative and a directory it does not name is
	// a directory falconet refuses to guess about; when it did not, Plan is
	// everything Discover found.
	Declared bool
}

// All is every stack in the layout, planned first, in order.
func (l Layout) All() []string {
	return append(append([]string{}, l.Plan...), l.Check...)
}

// Plans reports whether stack is one of the planned ones.
func (l Layout) Plans(stack string) bool { return contains(l.Plan, stack) }

// Resolve decides the layout from what the config named and what discovery
// found.
//
// Naming NEITHER list is how a repository says "you can see the shape of this
// as well as I can": every root module discovery found is planned, which is
// what a change to any of them deserves and what every other tool in this
// space does with an unconfigured repository. Naming EITHER list — including
// naming `validate_only` alone, which is how a repository says it plans
// nothing — makes the config authoritative, and a directory in neither list
// stops being falconet's to assume about.
func Resolve(discovered, plan, check []string) Layout {
	plan, check = nonEmpty(plan), nonEmpty(check)
	if len(plan) == 0 && len(check) == 0 {
		return Layout{Plan: nonEmpty(discovered)}
	}
	return Layout{Plan: plan, Check: check, Declared: true}
}

// Uses maps each local module directory to the stacks that reach it, directly
// or through another module. A module two stacks share appears under both.
func Uses(fsys fs.FS, stacks []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, s := range stacks {
		// Breadth-first from the stack, with the stack itself marked seen so
		// a module that sources its own parent cannot loop.
		seen := map[string]bool{s: true}
		queue := []string{s}
		for len(queue) > 0 {
			dir := queue[0]
			queue = queue[1:]
			targets, err := sources(fsys, dir)
			if err != nil {
				return nil, err
			}
			for _, t := range targets {
				if seen[t] {
					continue
				}
				seen[t] = true
				out[t] = append(out[t], s)
				queue = append(queue, t)
			}
		}
	}
	for _, s := range out {
		sort.Strings(s)
	}
	return out, nil
}

// Owner is the stack a repository-relative path lies in — the longest one
// that is a prefix of it — or "" for a path in none of them. A file in a
// module nested inside a stack belongs to that stack, which is why the
// longest match wins rather than the first.
func Owner(p string, all []string) string {
	best := ""
	for _, s := range all {
		if p == s || strings.HasPrefix(p, s+"/") {
			if len(s) > len(best) {
				best = s
			}
		}
	}
	return best
}

// Reach is which stacks a change reaches and which of its files no stack
// does.
//
//   - touched is the stacks, in the order All gave them: a stack holding a
//     changed file, or one that sources a module holding one.
//   - uncovered is the changed TERRAFORM files that reach no stack at all —
//     a `.tf` in a directory this repository does not treat as a stack, or
//     at the root, which is never one. Anything that is not a Terraform file
//     is left alone: a README outside every stack changes no plan.
//
// changed is the paths as git printed them, one per entry.
func Reach(changed []string, all []string, uses map[string][]string) (touched, uncovered []string) {
	hit := map[string]bool{}
	for _, p := range changed {
		if s := Owner(p, all); s != "" {
			hit[s] = true
			continue
		}
		if via := usedBy(p, uses); len(via) > 0 {
			for _, s := range via {
				hit[s] = true
			}
			continue
		}
		if isTerraform(p) {
			uncovered = append(uncovered, p)
		}
	}
	for _, s := range all {
		if hit[s] {
			touched = append(touched, s)
		}
	}
	return touched, uncovered
}

// usedBy is the stacks that source the module a path lies in, walking up from
// the path's own directory so a change to a module's non-`.tf` files — a
// template, a script it reads — reaches the same stacks its `.tf` files do.
func usedBy(p string, uses map[string][]string) []string {
	for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if s, ok := uses[dir]; ok {
			return s
		}
	}
	return nil
}

func isTerraform(p string) bool {
	for _, suffix := range terraformFile {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

// Intersect is the members of all that are in want, in all's order. The plan
// step uses it to keep the config's ordering of `stacks.plan` while planning
// only what the change reached.
func Intersect(all, want []string) []string {
	var out []string
	for _, s := range all {
		if contains(want, s) {
			out = append(out, s)
		}
	}
	return out
}

// Undeclared is the discovered stacks a declared layout names in neither
// list — the shape of #23 as a repository sitting still, before any request
// lands in one of them. Empty for an undeclared layout, which names nothing
// and so leaves nothing out.
func Undeclared(discovered []string, l Layout) []string {
	if !l.Declared {
		return nil
	}
	all := l.All()
	var out []string
	for _, s := range discovered {
		if !contains(all, s) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// nonEmpty drops empty entries, as the `[ -n "$_s" ]` in the read loops did.
func nonEmpty(list []string) []string {
	var out []string
	for _, s := range list {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
