// Package github is falconet's own GitHub client: net/http against
// GITHUB_API_URL, a token the caller hands in, and the handful of endpoints
// the verbs need. It replaces `gh` (ADR-0006 D2), so the runtime dependency
// set in CI becomes git, tofu, gitleaks and the binary — and on a
// workstation, the same.
//
// Nothing here retries, paginates or caches. A verb makes a call or three and
// reports each result, and a call that fails is an error carrying the status
// and the message GitHub sent, which is what a run log needs and all it
// needs.
//
// A 404 from GitHub means "not found" OR "no access": a private repository
// answers a token without permission exactly as it answers a name that does
// not exist, by design. The error says both, because both are true of what
// the caller knows (ADR-0005).
//
// The test suite points GITHUB_API_URL at tests/fixtures/fake-github.py, a
// loopback server that answers from fixtures and records what it was asked;
// this package's own tests use net/http/httptest the same way.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
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
// workflow hands the verbs (ADR-0006 D2). Setup's credential is a different
// variable with a different name on purpose (D4), and is not looked for here.
func TokenFromEnv() string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

// SplitRepository reads "owner/name" — the shape of $GITHUB_REPOSITORY — into
// its two halves. Anything else is an error naming what was expected.
func SplitRepository(s string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("%q is not an owner/name repository", s)
	}
	return owner, name, nil
}

// Client is one API endpoint and one token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New is a client for baseURL, authenticating with token. The timeout is per
// request: a verb that is parking an issue must not hang a job on a call
// that will never answer.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Error is GitHub saying no: the status it answered and the message it sent.
type Error struct {
	Method  string
	Path    string
	Status  int
	Message string
}

func (e *Error) Error() string {
	what := e.Message
	switch {
	case e.Status == http.StatusNotFound:
		what = "not found, or no access"
	case what == "":
		what = http.StatusText(e.Status)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, what)
}

// Do makes one request. in, when not nil, is sent as JSON; out, when not
// nil, is filled from a JSON response. Any status outside 2xx is an *Error.
func (c *Client) Do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%s %s: encoding the request: %v", method, path, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("%s %s: %v", method, path, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "falconet")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s %s: reading the response: %v", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Method: method, Path: path, Status: resp.StatusCode, Message: message(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: decoding the response: %v", method, path, err)
		}
	}
	return nil
}

// message is the "message" field of an error response, or nothing.
func message(raw []byte) string {
	var v struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.Message
}

func issuePath(owner, name string, number int, rest string) string {
	return fmt.Sprintf("/repos/%s/%s/issues/%d/%s",
		url.PathEscape(owner), url.PathEscape(name), number, rest)
}

// CreateIssueComment is POST /repos/{owner}/{name}/issues/{number}/comments.
func (c *Client) CreateIssueComment(owner, name string, number int, body string) error {
	return c.Do("POST", issuePath(owner, name, number, "comments"),
		map[string]string{"body": body}, nil)
}

// AddIssueLabels is POST /repos/{owner}/{name}/issues/{number}/labels: the
// labels are added to whatever the issue already carries.
func (c *Client) AddIssueLabels(owner, name string, number int, labels []string) error {
	return c.Do("POST", issuePath(owner, name, number, "labels"),
		map[string][]string{"labels": labels}, nil)
}

// RemoveIssueAssignees is DELETE /repos/{owner}/{name}/issues/{number}/assignees.
func (c *Client) RemoveIssueAssignees(owner, name string, number int, logins []string) error {
	return c.Do("DELETE", issuePath(owner, name, number, "assignees"),
		map[string][]string{"assignees": logins}, nil)
}
