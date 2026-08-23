// Package github is falconet's own GitHub client: net/http against
// GITHUB_API_URL, a token the caller hands in, and the handful of endpoints
// the verbs need. It replaces `gh` (ADR-0006 D2), so the runtime dependency
// set in CI becomes git, tofu, gitleaks and the binary — and on a
// workstation, the same.
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

// SetupTokenFromEnv is $FALCONET_SETUP_TOKEN, or empty. Setup's credential
// (ADR-0006 D4) is a token the operator mints — a fine-grained personal
// access token scoped to the one repository, with a seven-day expiry — and
// this is the only name it is read under.
//
// GITHUB_TOKEN and GH_TOKEN are deliberately NOT fallbacks. In CI they are
// the Actions token, which cannot do what setup does and must never be asked
// to; on a laptop they are whatever someone set for something else. A setup
// credential is powerful and short-lived, and its name should say which kind
// it is.
func SetupTokenFromEnv() string {
	return os.Getenv("FALCONET_SETUP_TOKEN")
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
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Reason())
}

// Reason is the status and GitHub's message, without the request — what a
// report line that already names the check wants to quote.
func (e *Error) Reason() string {
	what := e.Message
	switch {
	case e.Status == http.StatusNotFound:
		what = "not found, or no access"
	case what == "":
		what = http.StatusText(e.Status)
	}
	return what
}

// Do makes one request. in, when not nil, is sent as JSON; out, when not
// nil, is filled from a JSON response. Any status outside 2xx is an *Error.
func (c *Client) Do(method, path string, in, out any) error {
	_, err := c.Request(method, path, in, out)
	return err
}

// Request is Do, handing back the response headers as well. They are
// returned whenever GitHub answered at all — beside an *Error too — and are
// nil only when no response arrived. doctor reads X-OAuth-Scopes from them:
// a classic token reports its scopes there, a fine-grained token reports
// nothing, and a refusal carries the header just as an answer does.
func (c *Client) Request(method, path string, in, out any) (http.Header, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("%s %s: encoding the request: %v", method, path, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %v", method, path, err)
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
		return nil, fmt.Errorf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.Header, fmt.Errorf("%s %s: reading the response: %v", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.Header, &Error{Method: method, Path: path, Status: resp.StatusCode, Message: message(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.Header, fmt.Errorf("%s %s: decoding the response: %v", method, path, err)
		}
	}
	return resp.Header, nil
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
	if rest != "" {
		rest = "/" + rest
	}
	return fmt.Sprintf("/repos/%s/%s/issues/%d%s",
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

// Label is a repository label. Color and Description are optional on the
// way in (CreateLabel) and absent from nothing on the way out.
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

// Repository is the repository object: the fields README step 1 checks.
type Repository struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Owner         User   `json:"owner"`
	Private       bool   `json:"private"`
	Visibility    string `json:"visibility"`
	HasIssues     bool   `json:"has_issues"`
	DefaultBranch string `json:"default_branch"`
}

// ActionsPermissions is the repository's Actions policy: whether Actions is
// enabled, and which actions may run — "all", "local_only" or "selected".
type ActionsPermissions struct {
	Enabled        bool   `json:"enabled"`
	AllowedActions string `json:"allowed_actions"`
}

// SelectedActions is what "selected" allows: GitHub's own, verified
// creators', and a list of patterns.
type SelectedActions struct {
	GithubOwnedAllowed bool     `json:"github_owned_allowed"`
	VerifiedAllowed    bool     `json:"verified_allowed"`
	PatternsAllowed    []string `json:"patterns_allowed"`
}

// WorkflowPermissions is the default GITHUB_TOKEN grant — "read" or "write".
type WorkflowPermissions struct {
	DefaultWorkflowPermissions   string `json:"default_workflow_permissions"`
	CanApprovePullRequestReviews bool   `json:"can_approve_pull_request_reviews"`
}

// Secret is a repository secret, by name only: a value is never readable.
type Secret struct {
	Name string `json:"name"`
}

// PublicKey is the repository's secrets public key, which a value is sealed
// to before it is PUT. KeyID travels with the sealed value.
type PublicKey struct {
	KeyID string `json:"key_id"`
	Key   string `json:"key"`
}

// RepoPath is /repos/{owner}/{name}{rest}, with owner and name path-escaped.
// Exported for the one caller that builds a repository request by hand to
// read its headers (doctor), so that every call spells the repository the
// same way.
func RepoPath(owner, name, rest string) string {
	return fmt.Sprintf("/repos/%s/%s%s", url.PathEscape(owner), url.PathEscape(name), rest)
}

// --- reads -----------------------------------------------------------------

// GetIssue is GET /repos/{owner}/{name}/issues/{number}.
func (c *Client) GetIssue(owner, name string, number int) (*Issue, error) {
	var out Issue
	if err := c.Do("GET", issuePath(owner, name, number, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetIssueRaw is GetIssue's answer as GitHub sent it, undecoded. prepare
// writes the issue down as the one snapshot every later step reads, and a
// snapshot is the whole object rather than the fields this package happens
// to type; the caller decodes its typed view from the same bytes, so both
// views come from one fetch.
func (c *Client) GetIssueRaw(owner, name string, number int) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.Do("GET", issuePath(owner, name, number, ""), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListIssueComments is GET /repos/{owner}/{name}/issues/{number}/comments,
// one page of 100 in creation order. The 101st comment is not read.
func (c *Client) ListIssueComments(owner, name string, number int) ([]IssueComment, error) {
	var out []IssueComment
	if err := c.Do("GET", issuePath(owner, name, number, "comments?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListIssueCommentsRaw is ListIssueComments's answer as GitHub sent it, for
// the same snapshot GetIssueRaw serves. One page of 100; the 101st comment is
// not read.
func (c *Client) ListIssueCommentsRaw(owner, name string, number int) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.Do("GET", issuePath(owner, name, number, "comments?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListOpenPulls is GET /repos/{owner}/{name}/pulls?state=open, one page of
// 100 — wider than the 30 `gh pr list` read by default, and the 101st open
// pull request is not read.
func (c *Client) ListOpenPulls(owner, name string) ([]PullRequest, error) {
	var out []PullRequest
	if err := c.Do("GET", RepoPath(owner, name, "/pulls?state=open&per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAuthenticatedUser is GET /user: whose token this is.
func (c *Client) GetAuthenticatedUser() (*User, error) {
	var out User
	if err := c.Do("GET", "/user", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRepository is GET /repos/{owner}/{name}.
func (c *Client) GetRepository(owner, name string) (*Repository, error) {
	var out Repository
	if err := c.Do("GET", RepoPath(owner, name, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetActionsPermissions is GET /repos/{owner}/{name}/actions/permissions.
func (c *Client) GetActionsPermissions(owner, name string) (*ActionsPermissions, error) {
	var out ActionsPermissions
	if err := c.Do("GET", RepoPath(owner, name, "/actions/permissions"), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSelectedActions is GET /repos/{owner}/{name}/actions/permissions/selected-actions,
// which only means something when AllowedActions is "selected".
func (c *Client) GetSelectedActions(owner, name string) (*SelectedActions, error) {
	var out SelectedActions
	if err := c.Do("GET", RepoPath(owner, name, "/actions/permissions/selected-actions"), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkflowPermissions is GET /repos/{owner}/{name}/actions/permissions/workflow.
func (c *Client) GetWorkflowPermissions(owner, name string) (*WorkflowPermissions, error) {
	var out WorkflowPermissions
	if err := c.Do("GET", RepoPath(owner, name, "/actions/permissions/workflow"), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSecrets is GET /repos/{owner}/{name}/actions/secrets, one page of 100
// names (values are never readable). The 101st secret is not read.
func (c *Client) ListSecrets(owner, name string) ([]Secret, error) {
	var out struct {
		TotalCount int      `json:"total_count"`
		Secrets    []Secret `json:"secrets"`
	}
	if err := c.Do("GET", RepoPath(owner, name, "/actions/secrets?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out.Secrets, nil
}

// GetSecretsPublicKey is GET /repos/{owner}/{name}/actions/secrets/public-key.
func (c *Client) GetSecretsPublicKey(owner, name string) (*PublicKey, error) {
	var out PublicKey
	if err := c.Do("GET", RepoPath(owner, name, "/actions/secrets/public-key"), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListLabels is GET /repos/{owner}/{name}/labels, one page of 100. The 101st
// label is not read.
func (c *Client) ListLabels(owner, name string) ([]Label, error) {
	var out []Label
	if err := c.Do("GET", RepoPath(owner, name, "/labels?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// --- writes ----------------------------------------------------------------

// RemoveIssueLabel is DELETE /repos/{owner}/{name}/issues/{number}/labels/{label}.
func (c *Client) RemoveIssueLabel(owner, name string, number int, label string) error {
	return c.Do("DELETE", issuePath(owner, name, number, "labels/"+url.PathEscape(label)), nil, nil)
}

// AddIssueAssignees is POST /repos/{owner}/{name}/issues/{number}/assignees.
func (c *Client) AddIssueAssignees(owner, name string, number int, logins []string) error {
	return c.Do("POST", issuePath(owner, name, number, "assignees"),
		map[string][]string{"assignees": logins}, nil)
}

// CreateLabel is POST /repos/{owner}/{name}/labels. A label that already
// exists is a 422 from GitHub, which the caller decides about.
func (c *Client) CreateLabel(owner, name string, label Label) error {
	return c.Do("POST", RepoPath(owner, name, "/labels"), label, nil)
}

// PutSecret is PUT /repos/{owner}/{name}/actions/secrets/{secret}: the value,
// already sealed to the repository's public key and base64-encoded, and the
// key's id. Creating and updating are the same call.
func (c *Client) PutSecret(owner, name, secret, encryptedValue, keyID string) error {
	return c.Do("PUT", RepoPath(owner, name, "/actions/secrets/"+url.PathEscape(secret)),
		map[string]string{"encrypted_value": encryptedValue, "key_id": keyID}, nil)
}
