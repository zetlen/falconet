// Package github is falconet's adapter to the GitHub API: an interface the
// verbs depend on, the types GitHub answers with, and the handful of
// environment and URL helpers a verb needs to find its repository and its
// token. The one implementation shells out to the `gh` CLI (ghcli.go);
// nothing in the verbs knows that.
//
// Nothing here retries, paginates or caches. A verb makes a call or three and
// reports each result, and a call that fails is an error carrying the status
// and the message GitHub sent, which is what a run log needs and all it
// needs. The list reads ask for 100 per page and read one page; each says so
// in its own comment, so a caller that could be handed the 101st item knows
// it will not be.
//
// A 404 from GitHub means "not found" OR "no access": a private repository
// answers a token without permission exactly as it answers a name that does
// not exist, by design. The error says both, because both are true of what
// the caller knows.
//
// The test suite points GITHUB_API_URL at tests/fixtures/fake-github.py, a
// loopback server that answers from fixtures and records what it was asked;
// the gh adapter sends to that URL the same way it sends to api.github.com.
package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// DefaultAPIURL is api.github.com, which is what Actions sets GITHUB_API_URL
// to on github.com; a GitHub Enterprise Server run sets its own.
const DefaultAPIURL = "https://api.github.com"

// APIURLFromEnv is $GITHUB_API_URL, or the default, with no trailing slash.
func APIURLFromEnv() string {
	u := strings.TrimSpace(os.Getenv("GITHUB_API_URL"))
	if u == "" {
		u = DefaultAPIURL
	}
	return strings.TrimRight(u, "/")
}

// TokenFromEnv is $GH_TOKEN, or $GITHUB_TOKEN, or empty — the two names the
// workflow hands the verbs, and the two names `gh` reads.
func TokenFromEnv() string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

// ServerHostFromEnv is the host of $GITHUB_SERVER_URL — the variable Actions
// sets, "https://github.com" on github.com and an enterprise server's own
// URL there — or "github.com" when it is unset or does not parse.
func ServerHostFromEnv() string {
	if u, err := url.Parse(strings.TrimSpace(os.Getenv("GITHUB_SERVER_URL"))); err == nil && u.Host != "" {
		return u.Hostname()
	}
	return "github.com"
}

// SplitRepository reads "owner/name" — the shape of $GITHUB_REPOSITORY — into
// its two halves. Anything else is an error naming what was expected.
func SplitRepository(s string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || !repoWord(owner) || !repoWord(name) {
		return "", "", fmt.Errorf("%q is not an owner/name repository", s)
	}
	return owner, name, nil
}

// ParseRemoteURL reads owner and name out of a git remote URL that points at
// host — the three shapes git writes for a GitHub clone:
//
//	https://HOST/owner/name[.git][/]
//	git@HOST:owner/name[.git]
//	ssh://git@HOST[:port]/owner/name[.git]
//
// Credentials in the URL (https://user:token@HOST/…) are ignored, not
// compared. Hosts compare case-insensitively, as DNS does. Any other host, or
// a URL that does not reduce to owner/name, is an error: a verb that
// operates on "the repository this clone came from" must never guess one
// from a remote that points somewhere else.
func ParseRemoteURL(remote, host string) (owner, name string, err error) {
	remote = strings.TrimSpace(remote)
	var gotHost, path string
	switch {
	case strings.Contains(remote, "://"):
		u, perr := url.Parse(remote)
		if perr != nil {
			return "", "", fmt.Errorf("%q is not a URL: %v", remote, perr)
		}
		gotHost, path = u.Hostname(), u.Path
	default:
		// scp-like: [user@]HOST:path. The colon is the split, and there is
		// no slash before it. Only an '@' BEFORE the colon is a user: the
		// path after it may carry one (`host:o/r@v1` is a remote git
		// accepts), and taking the first '@' anywhere once sliced past the
		// colon and panicked.
		colon := strings.Index(remote, ":")
		if colon < 0 || strings.Contains(remote[:colon], "/") {
			return "", "", fmt.Errorf("%q is not a git remote URL", remote)
		}
		at := strings.LastIndex(remote[:colon], "@")
		gotHost, path = remote[at+1:colon], remote[colon+1:]
	}
	if !strings.EqualFold(gotHost, host) {
		return "", "", fmt.Errorf("%q is not on %s", remote, host)
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	owner, name, err = SplitRepository(path)
	if err != nil {
		return "", "", fmt.Errorf("%q does not name an owner/name repository on %s", remote, host)
	}
	return owner, name, nil
}

// repoWord is GitHub's alphabet for an owner or a repository name: letters,
// digits, '.', '_' and '-', and at least one of them. Anything else — a
// slash, a '?', an '@' — is refused here rather than path-escaped into a
// request that names a different repository than the one the caller spelled.
func repoWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// Client is the adapter: the verbs talk to GitHub through it.
type Client interface {
	GetIssue(owner, name string, number int) (*Issue, error)
	GetIssueRaw(owner, name string, number int) (json.RawMessage, error)
	ListIssueComments(owner, name string, number int) ([]IssueComment, error)
	ListIssueCommentsRaw(owner, name string, number int) (json.RawMessage, error)
	ListOpenPulls(owner, name string) ([]PullRequest, error)
	GetAuthenticatedUser() (*User, error)
	CreateIssueComment(owner, name string, number int, body string) error
	AddIssueLabels(owner, name string, number int, labels []string) error
	RemoveIssueLabel(owner, name string, number int, label string) error
	AddIssueAssignees(owner, name string, number int, logins []string) error
	RemoveIssueAssignees(owner, name string, number int, logins []string) error
}

// Error is GitHub saying no: the status it answered and the message it sent.
type Error struct {
	Method  string
	Path    string
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.reason())
}

func (e *Error) reason() string {
	what := e.Message
	switch {
	case e.Status == http.StatusNotFound:
		what = "not found, or no access"
	case what == "":
		what = http.StatusText(e.Status)
	}
	return what
}

// --- the shapes GitHub answers with ------------------------------------------
//
// Each type carries the fields a verb reads and no more; the tags are the
// keys as GitHub spells them. A field GitHub adds later is ignored, and a
// field missing from an answer is its zero value — neither is an error,
// because the verbs decide on what is there.

// User is an account: the author of an issue or comment, an assignee, or the
// token's owner.
type User struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// Label is a repository label, as an issue carries it.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
}

// Issue is an issue — or a pull request, which the issues endpoint also
// answers for: PullRequest is non-nil exactly when the "issue" is one.
type Issue struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	State       string  `json:"state"`
	Labels      []Label `json:"labels"`
	User        User    `json:"user"`
	Assignees   []User  `json:"assignees"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

// IssueComment is one comment on an issue.
type IssueComment struct {
	User      User   `json:"user"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

// PullRequest is the part of a pull request the gate reads: its number and
// the branch it comes from.
type PullRequest struct {
	Number int `json:"number"`
	Head   struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

// IssuePath builds /repos/{owner}/{name}/issues/{number}[/rest], with owner
// and name path-escaped.
func IssuePath(owner, name string, number int, rest string) string {
	if rest != "" {
		rest = "/" + rest
	}
	return fmt.Sprintf("/repos/%s/%s/issues/%d%s",
		url.PathEscape(owner), url.PathEscape(name), number, rest)
}

// RepoPath is /repos/{owner}/{name}{rest}, with owner and name path-escaped,
// so that every call spells the repository the same way.
func RepoPath(owner, name, rest string) string {
	return fmt.Sprintf("/repos/%s/%s%s", url.PathEscape(owner), url.PathEscape(name), rest)
}

// Message extracts the "message" field from a JSON error response, or "".
func Message(raw []byte) string {
	var v struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.Message
}
