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
)

// Root returns the repository root: $FALCONET_REPO if set (it must name a
// directory, and is returned with symlinks resolved, as `pwd -P` would);
// otherwise the enclosing git work tree; otherwise cwd itself. Not in a git
// repository is not an error here — some verbs need one and say so in their
// own words; assemble and prompt do not, and should still work.
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
