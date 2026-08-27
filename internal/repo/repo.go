// Package repo answers one question: which repository is this verb operating
// on?
//
// The origin's scripts lived INSIDE the repository they worked on, so "one
// directory above scripts/" answered two questions at once: where the code
// is, and where the work is. falconet is a separate tool — in CI a binary
// installed on the runner, on a workstation a binary on $PATH — and the two
// answers come apart. A verb that used its own location to find the working
// tree would operate on falconet instead of on the consumer's repository,
// silently, reporting an outcome about the wrong tree.
//
// So the repository being worked on comes from the working directory, which
// is the only thing that can know, and never from where the binary lives.
// $FALCONET_REPO overrides, for a caller that wants to name it explicitly.
package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zetlen/falconet/internal/github"
)

// Root returns the repository root: $FALCONET_REPO if set (it must name a
// directory, and is returned with symlinks resolved, as `pwd -P` would);
// otherwise the enclosing git work tree; otherwise cwd itself. Not in a git
// repository is not an error here — some verbs need one and say so in their
// own words; prompt does not, and should still work.
func Root(cwd string) (string, error) {
	if r := os.Getenv("FALCONET_REPO"); r != "" {
		info, err := os.Stat(r)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("$FALCONET_REPO names no directory: %s", r)
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			return "", fmt.Errorf("$FALCONET_REPO cannot be resolved: %s: %v", r, err)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("$FALCONET_REPO cannot be resolved: %s: %v", r, err)
		}
		return resolved, nil
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if top := strings.TrimSpace(string(out)); err == nil && top != "" {
		return top, nil
	}
	return cwd, nil
}

// Repository answers the other question a verb that talks to GitHub has to
// ask: which GitHub repository is this tree a clone of? $GITHUB_REPOSITORY
// if set — Actions sets it in every run, and a local run may export it —
// else the `origin` remote of the repository at cwd, parsed for an
// owner/name on github.com (or on the host $GITHUB_SERVER_URL names).
//
// This is for the verbs that operate on the tree they stand in — doctor and
// prepare — where the tree's origin is the right answer. pause does not use
// it and must not: it operates on an issue, not a tree, and a comment on a
// guessed repository is the failure its GITHUB_REPOSITORY-only rule exists
// to prevent.
//
// No remote, a remote on another host, or a URL that does not reduce to
// owner/name is an error naming both sources, so the operator knows what
// either fix looks like.
func Repository(cwd string) (owner, name string, err error) {
	if r := os.Getenv("GITHUB_REPOSITORY"); r != "" {
		owner, name, err = github.SplitRepository(r)
		if err != nil {
			return "", "", fmt.Errorf("$GITHUB_REPOSITORY %v", err)
		}
		return owner, name, nil
	}
	host := github.ServerHostFromEnv()
	fix := fmt.Sprintf("set GITHUB_REPOSITORY=owner/name, or run from a clone whose origin is on %s", host)
	cmd := exec.Command("git", "-C", cwd, "remote", "get-url", "origin")
	out, err := cmd.Output()
	remote := strings.TrimSpace(string(out))
	if err != nil || remote == "" {
		return "", "", fmt.Errorf("cannot tell which GitHub repository this is: no origin remote; %s", fix)
	}
	owner, name, err = github.ParseRemoteURL(remote, host)
	if err != nil {
		return "", "", fmt.Errorf("cannot tell which GitHub repository this is: origin %v; %s", err, fix)
	}
	return owner, name, nil
}
