// Package handoff is the directory the verbs talk through, and the one place
// that knows $GITHUB_ENV is optional.
//
// Verbs never call each other. They leave files for each other in
// handoff_dir (default .falconet/, gitignored), exactly as the stage scripts
// always did, which is what lets the identical sequence run on a workstation
// with no GitHub context around it.
//
// If you move handoff_dir, gitignore the new location. The commit verb's path
// allowlist looks at everything the working tree reports as changed, so an
// un-ignored handoff directory turns into a run that refuses its own scratch
// files and names them in the failure a requester reads. The allowlist is
// right to do that; the fix is the .gitignore entry, and the entry is the
// first line of the defence anyway (a `git add -A` cannot pick up an ignored
// path).
package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zetlen/falconet/internal/config"
)

// Init resolves the handoff directory and creates it. An explicit override —
// the --out-dir flag several verbs carry — wins over the config, and a
// relative path is resolved against cwd, which for a verb is the repository
// root: verbs cd there before they read config, and a relative path would
// otherwise mean somewhere else.
func Init(explicit string, cfg *config.Config, cwd string) (string, error) {
	dir := explicit
	if dir == "" {
		dir = cfg.Schema.HandoffDir
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create handoff directory %s", dir)
	}
	return dir, nil
}

// GitHubEnvAppend appends KEY=value lines to $GITHUB_ENV when there is one
// and it can be written, and is a silent no-op otherwise. Handoff FILES are
// written always; this is only the CI mirror of them. A verb that made a
// decision must not fail because it happens to be running on a laptop —
// which is why nothing here returns an error.
func GitHubEnvAppend(lines ...string) {
	path := os.Getenv("GITHUB_ENV")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(strings.Join(lines, "\n") + "\n")
}
