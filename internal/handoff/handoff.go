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
	"regexp"
	"strings"

	"github.com/zetlen/falconet/internal/config"
)

// Resolve is where the handoff directory is, without creating it. An
// explicit override — the --out-dir flag several verbs carry — wins over the
// config, and a relative path is resolved against cwd, which for a verb is
// the repository root: verbs cd there before they read config, and a
// relative path would otherwise mean somewhere else.
//
// On its own it is for a verb that only names the directory: the prompt verb
// substitutes it into the text it prints, and printing a prompt is a read —
// a caller asking what the text says should not leave a directory behind.
func Resolve(explicit string, cfg *config.Config, cwd string) string {
	dir := explicit
	if dir == "" {
		dir = cfg.Schema.HandoffDir
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return dir
}

// Init resolves the handoff directory and creates it.
func Init(explicit string, cfg *config.Config, cwd string) (string, error) {
	dir := Resolve(explicit, cfg, cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create handoff directory %s", dir)
	}
	return dir, nil
}

// GitHubEnvAppend appends KEY=value lines to $GITHUB_ENV when there is one
// and it can be written, and is a silent no-op otherwise. Handoff FILES are
// written always; this is only the CI mirror of them. A verb that made a
// decision must not fail because it happens to be running on a laptop —
// which is why a missing or unwritable $GITHUB_ENV is not an error.
//
// A malformed line IS an error, and is refused before anything is written.
// Actions reads $GITHUB_ENV line by line, so a value carrying a newline would
// become further KEY=value lines: arbitrary environment in every later step
// of the job, chosen by whoever chose the value — and the values that travel
// this way are branch names, which come from issue titles. The bash verbs
// slugify upstream (prepare.sh); this refuses here as well, because the one
// function that writes to $GITHUB_ENV must not depend on every future caller
// remembering to.
func GitHubEnvAppend(lines ...string) error {
	for _, line := range lines {
		if err := checkLine(line); err != nil {
			return err
		}
	}
	path := os.Getenv("GITHUB_ENV")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(strings.Join(lines, "\n") + "\n")
	return nil
}

// envKey is what Actions accepts as a variable name, and nothing else: an
// identifier. A key never comes from input, so a bad one is a bug in a
// caller, and is still refused rather than written.
var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func checkLine(line string) error {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return fmt.Errorf("refusing to write to $GITHUB_ENV: %q is not KEY=value", line)
	}
	if !envKey.MatchString(key) {
		return fmt.Errorf("refusing to write to $GITHUB_ENV: %q is not a variable name", key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("refusing to write to $GITHUB_ENV: the value of %s contains a line break, "+
			"which would become further variables in every later step", key)
	}
	return nil
}
