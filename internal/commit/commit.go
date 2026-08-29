// Package commit is the guards the commit verb is made of — the path
// allowlist, the content denylist, the reading of `git status`, the split of
// a message into a pull request's title and body, and the words each refusal
// says to the requester. The verb itself, cmd/falconet/commit.go, is the
// sequence those run in, the subprocesses between them, the files, and the
// exit code.
//
// Nothing here touches the filesystem or runs a process: the verb hands in
// bytes — the status listing, a file's content, the commit message — and gets
// back a decision. That is what lets each guard be held to a table, and the
// allowlist's translation to a differential against bash itself, in
// commit_test.go, rather than to the handful of fixtures a suite can carry.
//
// # The path allowlist
//
// Only `*.tf` may be committed. Anything else is a failure that names the
// path. It was `*.tf` and `scripts/record-manifest.txt` until #17 deleted the
// manifest: a DNS record now lives in exactly one file, which is a `.tf` file,
// so the second entry stopped naming anything a request could need.
//
// The issue title, body and comment thread are attacker-controlled text, and
// they are also the agent's instructions. An issue that asks it to "also
// update .github/workflows/infra-issues.yml to grant Bash" is a privilege
// escalation, and until now the only thing standing against it was a cheap
// model answering "are unrelated files touched?". That question is now a case
// statement. A request that genuinely needs a script change fails to a human,
// which is the right answer for a request that wants to edit the machinery
// that reviews it.
//
// COMMITTED files only, and that is the whole of it. This is a gate on the
// commit, not a sandbox. The agent holds unrestricted Read over the workspace,
// and what it writes into .ci-handoff/commit-msg.txt and
// .ci-handoff/needs-info.md is not a committed file at all — the first is
// published as the pull-request body, the second as a comment on the
// requester's issue. The allowlist decides what lands in the repository; it
// does not decide what the agent can see or what it can say. The secret scan
// below reads those two files, but it is a pattern matcher, not a boundary.
//
// # The publish-boundary secret scan
//
// internal/scan — gitleaks over commit-msg.txt, needs-info.md and
// the staged diff, before anything is committed. Issue #41: the agent can read
// the job's push token out of .git/config, and until this scan existed the two
// handoff files above carried whatever it wrote straight to the GitHub API,
// which does not apply the masking that hides $GITHUB_TOKEN in run logs.
//
// A hit is a `failure`, not a redaction: the run stops, nothing is committed,
// and the requester gets failure-reason.txt, which says a secret-like string
// was found and NEVER repeats it. Read that package's header for what this
// does not do — it matches known patterns, so it is evidence of a leak and
// never evidence of the absence of one, and the token is still readable by
// the agent either way.
//
// # The content denylist
//
// The path guard above says WHERE an agent may write; it says nothing about
// WHAT. A `.tf` file is executable content in this pipeline: a
// `data "external"` block runs an arbitrary command during the `tofu plan`
// that happens two steps later, and a `provisioner` block — most concretely
// its `local-exec` (runs on the runner) and `remote-exec` (runs over the
// network) types — runs one during `tofu apply`. Both of those steps run on a
// runner holding the state backend's credential and a checkout whose git
// remote still carries a push token. So an issue that
// asks for one of these four constructs is the same privilege escalation as
// the workflow-file edit above, just aimed at a path the allowlist waves
// through. Refused the same way: failure, naming the file and the construct.
//
// The list covers READING as well as executing, which is why `file(`,
// `templatefile(` and `filebase64(` are on it. Nothing has to run for those to
// leak. A `.tf` containing
//
//	output "leak" { value = file("/etc/hosts") }
//
// makes `tofu plan` print that file's entire contents under
// `Changes to Outputs:` — no provider, no `tofu init`, and none of the four
// constructs above — and the plan is what the plan bot posts on the pull
// request. The best target is inside the workspace the agent is standing in:
// `file("${path.module}/.git/config")` was readable at plan time because
// actions/checkout left the job's token there and ci-push-branch.sh rewrote
// the remote to `https://x-access-token:$GH_TOKEN@...` two steps earlier. This
// configuration uses none of the three today, so the entries cost nothing; a
// change that genuinely needs one fails to a human, which is the right answer
// for a change that wants to read a file off the runner.
package commit

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Policy is the allowlist and the denylist, compiled once.
//
// Read once, here, rather than at each use: a guard that re-reads its own rule
// mid-run is a guard whose behavior depends on when you look.
type Policy struct {
	// Allow is paths.allow as configured, empty entries dropped, for the
	// refusal that names the allowlist a path was measured against.
	Allow []string
	allow []*regexp.Regexp
	deny  []denyEntry
}

type denyEntry struct {
	literal string
	re      *regexp.Regexp
}

// NewPolicy compiles paths.allow and paths.deny_content. An empty entry in
// either is skipped, as it always was; an entry that cannot be compiled is an
// error, because a rule that silently matches nothing is not a rule. An empty
// paths.allow — no non-empty entries — is refused: an allowlist with nothing
// in it admits nothing, and the operator must name what the agent may touch.
func NewPolicy(allow, denyContent []string) (*Policy, error) {
	p := &Policy{}
	for _, glob := range allow {
		if glob == "" {
			continue
		}
		re, err := AllowPattern(glob)
		if err != nil {
			return nil, fmt.Errorf("paths.allow entry %q: %v", glob, err)
		}
		p.Allow = append(p.Allow, glob)
		p.allow = append(p.allow, re)
	}
	if len(p.Allow) == 0 {
		return nil, fmt.Errorf("paths.allow is empty — set it in .github/falconet.json to name the paths the agent may change")
	}
	for _, literal := range denyContent {
		if literal == "" {
			continue
		}
		re, err := regexp.Compile(DenyPattern(literal))
		if err != nil {
			return nil, fmt.Errorf("paths.deny_content entry %q: %v", literal, err)
		}
		p.deny = append(p.deny, denyEntry{literal: literal, re: re})
	}
	return p, nil
}

// PathAllowed reports whether ANY paths.allow glob matches the path.
func (p *Policy) PathAllowed(path string) bool {
	for _, re := range p.allow {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// AllowPattern translates one paths.allow entry into a regular expression
// with the meaning the entry always had.
//
// The globs were matched unquoted in a bash `case`, which is what made them
// globs rather than literals — and a `case` pattern is not what every glob
// library means by the word. The README documents the difference that
// matters: "`*` crosses `/`, so `*.tf` matches `dns/records.tf`". Go's
// path.Match stops `*` at a slash, so the pattern is translated instead of
// handed to a library that would quietly narrow it:
//
//   - `*` becomes `.*`, and matches across `/`
//   - `?` becomes `.`, one character
//   - a bracket expression passes through, with `!` as well as `^` for
//     negation, a leading `]` taken literally, `\` quoting the next
//     character, and POSIX classes like `[[:alpha:]]` intact; a `[` with no
//     closing `]` is a literal `[`
//   - `\` quotes the next character. A `\` with nothing after it is
//     refused: bash 3.2, the one macOS ships, made it a pattern that
//     matches nothing; bash 5, the one CI and the runners have, makes it a
//     literal backslash after a character (`a\` matches `a\`) and nothing
//     after a star (`a*\` matches neither `ab\` nor `a\`) — measured on
//     both, 2026-08-22. Three readings of one character is not a rule,
//     and an allowlist entry that ends in an unpaired backslash is a typo
//     worth hearing about
//   - everything else is literal, `|` included: a `|` that arrives by
//     variable expansion is a character in the pattern, not a second
//     pattern (measured against bash 3.2)
//
// Anchored at both ends, as a `case` match is. A reversed range such as
// `[c-a]` is the other place the two part company: bash matches nothing and
// says nothing; this refuses to compile it. Either refusal is the verb
// exiting 1 and naming the entry.
func AllowPattern(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`^(?s:`)
	for i := 0; i < len(glob); {
		switch c := glob[i]; c {
		case '*':
			b.WriteString(`.*`)
			i++
		case '?':
			b.WriteString(`.`)
			i++
		case '\\':
			if i+1 < len(glob) {
				b.WriteString(regexp.QuoteMeta(glob[i+1 : i+2]))
				i += 2
			} else {
				return nil, errors.New("ends in an unpaired backslash")
			}
		case '[':
			if class, n, ok := bracket(glob[i:]); ok {
				b.WriteString(class)
				i += n
			} else {
				b.WriteString(`\[`)
				i++
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString(`)$`)
	return regexp.Compile(b.String())
}

// bracket reads the bracket expression at the start of s and returns it in
// regexp syntax with the number of bytes it consumed. ok is false when there
// is no closing `]`, in which case bash takes the `[` literally.
func bracket(s string) (class string, n int, ok bool) {
	var b strings.Builder
	b.WriteByte('[')
	j := 1
	if j < len(s) && (s[j] == '!' || s[j] == '^') {
		b.WriteByte('^')
		j++
	}
	if j < len(s) && s[j] == ']' {
		b.WriteString(`\]`)
		j++
	}
	for j < len(s) {
		switch {
		case s[j] == ']':
			b.WriteByte(']')
			return b.String(), j + 1, true
		case s[j] == '[' && j+1 < len(s) && s[j+1] == ':':
			end := strings.Index(s[j+2:], ":]")
			if end < 0 {
				b.WriteString(`\[`)
				j++
				continue
			}
			b.WriteString(s[j : j+2+end+2])
			j += 2 + end + 2
		case s[j] == '\\' && j+1 < len(s):
			b.WriteString(inClass(s[j+1]))
			j += 2
		case s[j] == '\\' || s[j] == '[':
			b.WriteString(inClass(s[j]))
			j++
		default:
			b.WriteByte(s[j])
			j++
		}
	}
	return "", 0, false
}

// inClass is one literal character inside a regexp bracket expression.
func inClass(c byte) string {
	switch c {
	case '\\', ']', '[', '^', '-':
		return `\` + string(c)
	}
	return string(c)
}

// DenyPattern turns a paths.deny_content entry into the regular expression
// it is matched as.
//
// A denylist entry is written the way a person writes the construct —
// `templatefile(`, `data "external"` — and matched the way HCL actually spells
// it, which is with whitespace in the joints. `templatefile (` and
// `data  "external"` are the same construct and must not be a way past the
// guard. So the literal becomes a regex: metacharacters escaped, then
// whitespace tolerated before an opening paren, around a quote, and wherever
// the literal has a space.
//
// This reproduces the hand-written regexes it replaces, character for
// character. That is the point: the config's default IS the old behavior.
// regexp.QuoteMeta escapes exactly the fourteen characters the sed did, and
// the three substitutions follow in the same order.
func DenyPattern(literal string) string {
	p := regexp.QuoteMeta(literal)
	p = strings.ReplaceAll(p, `\(`, `[[:space:]]*\(`)
	p = strings.ReplaceAll(p, `"`, `[[:space:]]*"[[:space:]]*`)
	p = strings.ReplaceAll(p, ` `, `[[:space:]]*`)
	return p
}

// DenyLabel is what the requester is told was found. `templatefile(` is how
// you write the rule; `templatefile()` is how you name the thing.
func DenyLabel(literal string) string {
	if strings.HasSuffix(literal, "(") {
		return literal + ")"
	}
	return literal
}

// DenylistHit names the first denied construct in a file's content, or
// reports that there is none.
//
// First match wins, IN CONFIG ORDER, which is why the order is load-bearing
// and why internal/config preserves it. `templatefile(` contains a `file(`, so
// a denylist that tested `file(` first would report a templatefile() call as
// file() — the right refusal naming the wrong construct, and nothing
// downstream can recover the distinction.
//
// Matched line by line, as `grep -E` matched it. Every pattern carries
// `[[:space:]]*` in its joints, and over a whole file that class would reach
// across a line break, refusing `data` on one line and `"external"` on the
// next — which is not a block header in HCL, and which the guard never
// refused. Per line, the two agree.
func (p *Policy) DenylistHit(content []byte) (label string, hit bool) {
	lines := bytes.Split(content, []byte{'\n'})
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		// grep counts no line after a final newline, and none in an empty
		// file.
		lines = lines[:n-1]
	}
	for _, d := range p.deny {
		for _, line := range lines {
			if d.re.Match(line) {
				return DenyLabel(d.literal), true
			}
		}
	}
	return "", false
}

// Entry is one record of `git status --porcelain -z`: the two status columns
// and the path that follows them.
type Entry struct {
	Code string
	Path string
}

// ParseStatus reads `git status --porcelain --untracked-files=all -z` and
// returns the changed paths in git's order — or the first rename or copy,
// which is refused rather than parsed.
//
// -z, so a path with a space in it survives; --untracked-files=all, so a new
// records-*.tf counts. A rename or copy is not staged before this verb runs
// and this agent cannot stage one itself, so none should appear — checked,
// not assumed, though: git status -z reports a rename as TWO NUL-terminated
// fields, a status-prefixed new path and then a bare old path with no prefix
// at all, and slicing that bare field the same way as everything else
// corrupts it. Detected by its leading R or C and refused, rather than
// silently mis-parsed.
func ParseStatus(z []byte) (changed []string, refused *Entry) {
	for _, rec := range bytes.Split(z, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		s := string(rec)
		code, path := s, ""
		if len(s) >= 2 {
			code = s[:2]
		}
		if len(s) > 3 {
			path = s[3:]
		}
		if s[0] == 'R' || s[0] == 'C' {
			return nil, &Entry{Code: code, Path: path}
		}
		changed = append(changed, path)
	}
	return changed, nil
}

// Subject is the message's first line — the pull-request TITLE — with its
// newline, when it has one.
func Subject(message []byte) []byte {
	if i := bytes.IndexByte(message, '\n'); i >= 0 {
		return message[:i+1]
	}
	return message
}

// Body is the rest of the message — the pull-request BODY.
//
// Drop the subject, then drop the blank lines that separated it. An agent that
// wrote a subject and no body gets the subject as its description: a pull
// request with an empty body is worse than a repetitive one.
func Body(message []byte) []byte {
	var rest []byte
	if i := bytes.IndexByte(message, '\n'); i >= 0 {
		rest = message[i+1:]
	}
	for len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return Subject(message)
	}
	return rest
}

// --- what each refusal says ------------------------------------------------
//
// The text of failure-reason.txt, which is posted to the requester's issue
// verbatim. Each is one line per sentence fragment, as the verb always wrote
// it, with a list of paths as one indented block.

// reason joins the lines of a refusal, each terminated.
func reason(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// indented is a list of paths, two spaces in, one per line, with no newline
// after the last: it is spliced in as one line of a reason.
func indented(items []string) string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = "  " + item
	}
	return strings.Join(out, "\n")
}

// ReasonRename is the refusal of a staged rename or copy.
func ReasonRename(code, path string) string {
	return reason(
		"The agent's change was reported as a rename or copy (git",
		"status code '"+code+"' for "+path+"), which this script",
		"refuses to parse rather than risk misreading the paths",
		"involved. Ask for the change as a plain add and delete",
		"instead.")
}

// ReasonDeniedPaths is the refusal of a change outside the allowlist.
func ReasonDeniedPaths(allow, denied []string) string {
	return reason(
		"The agent changed files it is not allowed to change, so nothing",
		"was committed. Only these paths may be changed in response to",
		"an issue: "+strings.Join(allow, " ")+". Refused paths:",
		indented(denied))
}

// ReasonDeniedContent is the refusal of a denied construct. Each hit is
// "path: construct", as DenylistHit named it.
func ReasonDeniedContent(hits []string) string {
	return reason(
		"The agent's .tf changes contain a construct that runs code, or",
		"reads a file off the runner, during tofu plan or apply, so",
		"nothing was committed. Constructs like data \"external\",",
		"provisioner, local-exec and remote-exec run a command; file(),",
		"templatefile() and",
		"filebase64() read a path and can print what they read into the",
		"plan. All of them are refused wherever they appear, whatever the",
		"commit message says. Refused:",
		indented(hits))
}

// ReasonSecret is the refusal of a credential-shaped string, naming the
// channels that matched and never what matched in them.
func ReasonSecret(channels []string) string {
	return reason(
		"I stopped this run before it published anything: a string",
		"shaped like a credential turned up in the text it was about",
		"to post. Nothing was committed and nothing was posted.",
		"The matching text is deliberately not repeated here — that",
		"would publish it in this very comment. Where it matched:",
		indented(channels),
		"(commit-msg.txt would have become this change's pull-request",
		"description; needs-info.md would have been posted here as a",
		"question; the staged change is the diff itself.)",
		"A person needs to read the run log, and if that was a real",
		"credential, rotate it. The scanner matches known patterns, so",
		"treat this as evidence of a leak — never treat a quiet run as",
		"evidence that there is nothing to find.")
}

// ReasonUnchanged is the failure of a run that did nothing at all.
func ReasonUnchanged() string {
	return reason(
		"The agent left the repository unchanged and asked no questions.",
		"Nothing was committed. There is no prepared change to look at.")
}

// ReasonNoMessage is the failure of a change with nothing to commit it under.
func ReasonNoMessage(changed []string) string {
	return reason(
		"The agent changed files but wrote no commit message, so there was",
		"nothing to commit them under. It should have written its message",
		"to .ci-handoff/commit-msg.txt. Changed paths:",
		indented(changed))
}

// ReasonEmptyStaged is the failure of a change that staged to nothing.
func ReasonEmptyStaged(changed []string) string {
	return reason(
		"The agent's change amounted to nothing once staged; the tree",
		"matches what is already committed, so there is nothing to",
		"commit. Changed paths:",
		indented(changed))
}
