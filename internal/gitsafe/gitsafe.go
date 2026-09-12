// Package gitsafe runs git against a working tree the agent controls, with
// the configuration-driven ways git can be made to run a program shut off.
//
// The guards in the commit path — the secret scan, the status read, the
// staging, the commit — all shell out to git inside the agent's own
// checkout. A harness with only file tools can still write into that
// checkout's .git: .git/config, .git/hooks and .git/info/attributes are all
// reachable by an ordinary file write, and none of them is visible to the
// `git status` the path allowlist reads. Several git settings name a program
// git then runs during an everyday operation — a status, a diff, a stage, a
// commit — which are exactly the operations the guards perform. So without
// this a file-only agent could reach a shell inside the implement job, whose
// one secret is the model key, before any guard could refuse a thing.
//
// Two defences live here, and a caller uses both:
//
//   - Command builds every git invocation with those channels overridden, so
//     even a setting that slips past the check below runs nothing.
//   - Untrusted refuses outright a checkout whose git machinery carries any
//     such setting, hook or attributes file, because that machinery is never
//     the agent's to set and a clear refusal beats a silently neutralised
//     attack. The commit verb calls it, so the refusal reaches the requester
//     as an ordinary failure.
package gitsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hardening is the -c overrides prepended to every git command: the settings
// that name a program to run during an ordinary operation, each set empty so
// a value in the checkout's own config cannot win — a command-line -c
// outranks local config. The diff- and commit-specific channels that no
// single -c can name (an external diff, a textconv, the commit hooks) are
// closed by the --no-ext-diff, --no-textconv and --no-verify flags the
// callers pass.
var hardening = []string{
	"-c", "core.fsmonitor=",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.attributesFile=/dev/null",
}

// scrubbed is the process environment with the global and system git config —
// and thus their own hooks, attributes and execution settings — pointed at
// nothing, so only the checkout's own config is read at all.
func scrubbed() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

// Command is exec.Command("git", …) for a tree the agent controls: it runs
// in dir, reads no global or system config, and overrides the config keys
// that would otherwise make an ordinary git operation execute a program.
// Callers add --no-ext-diff --no-textconv to a diff and --no-verify to a
// commit, the channels a bare -c cannot name.
func Command(dir string, args ...string) *exec.Cmd {
	full := append([]string{"-C", dir}, hardening...)
	full = append(full, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = scrubbed()
	return cmd
}

// Untrusted reports a checkout whose own git machinery would run a program
// when a guard runs git over it — the one thing a file-only agent must not be
// able to arrange. It reads the checkout's effective configuration (global
// and system scrubbed, so only what the checkout carries) and looks at the
// on-disk hook and attribute files; it never runs anything the configuration
// names. A non-empty return is the reason to refuse; "" is a clean tree.
func Untrusted(dir string) string {
	if reason := untrustedConfig(dir); reason != "" {
		return reason
	}
	return untrustedFiles(dir)
}

func untrustedConfig(dir string) string {
	// Plain --list, not the -c-injecting Command: the overrides above would
	// appear here as their own (empty) keys and read as findings. Only a key
	// the checkout sets, with a value, is a finding.
	cmd := exec.Command("git", "-C", dir, "config", "--list", "-z")
	cmd.Env = scrubbed()
	out, err := cmd.Output()
	if err != nil {
		// No config to read is not a finding; a git that cannot run at all is
		// caught by the caller when it runs git for real.
		return ""
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		if value == "" {
			continue
		}
		if dangerousKey(key) {
			return "the checkout's git config sets " + key
		}
	}
	return ""
}

// dangerousKey is a config key whose value git runs as a command during an
// operation a guard performs. git lower-cases the section and name in
// --list, so the comparisons are lower-case.
func dangerousKey(key string) bool {
	switch key {
	case "core.fsmonitor", "core.hookspath", "core.sshcommand", "core.pager", "diff.external":
		return true
	}
	// [diff "x"] command / textconv, and [filter "x"] clean/smudge/process:
	// the driver name is the caller's, so match by shape.
	if strings.HasPrefix(key, "diff.") && (strings.HasSuffix(key, ".command") || strings.HasSuffix(key, ".textconv")) {
		return true
	}
	if strings.HasPrefix(key, "filter.") &&
		(strings.HasSuffix(key, ".clean") || strings.HasSuffix(key, ".smudge") || strings.HasSuffix(key, ".process")) {
		return true
	}
	return false
}

func untrustedFiles(dir string) string {
	gitDir := resolveGitDir(dir)
	if gitDir == "" {
		return ""
	}
	// .git/info/attributes binds diff drivers and filters from a file the
	// path allowlist never sees.
	if info, err := os.Stat(filepath.Join(gitDir, "info", "attributes")); err == nil && !info.IsDir() {
		return "the checkout carries .git/info/attributes"
	}
	// A hook is a program git runs; the samples git ships end in .sample and
	// run nothing, so only a non-sample file is a finding.
	entries, err := os.ReadDir(filepath.Join(gitDir, "hooks"))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".sample") {
			continue
		}
		return "the checkout carries the git hook " + e.Name()
	}
	return ""
}

// resolveGitDir is the checkout's git directory, usually dir/.git but not
// always (a worktree, a gitdir pointer). "" when it cannot be found, which
// untrustedFiles reads as nothing on disk to check.
func resolveGitDir(dir string) string {
	out, err := Command(dir, "rev-parse", "--git-dir").Output()
	if err != nil {
		return ""
	}
	g := strings.TrimSpace(string(out))
	if g == "" {
		return ""
	}
	if !filepath.IsAbs(g) {
		g = filepath.Join(dir, g)
	}
	return g
}
