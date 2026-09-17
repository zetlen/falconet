// Package github is falconet's adapter to the GitHub API: GH, the
// forge.Client that shells out to the `gh` CLI, and DecodeEvent, the reader
// that turns a GitHub webhook payload into what the gate reads. Nothing in
// the verbs knows the client is `gh`.
//
// Nothing here retries, paginates or caches. A verb makes a call or three and
// reports each result, and a call that fails is a *forge.Error carrying the
// status and the message GitHub sent, which is what a run log needs and all
// it needs. The list reads ask for 100 per page and read one page; each says
// so in its own comment, so a caller that could be handed the 101st item
// knows it will not be.
//
// The test suite points GITHUB_API_URL at tests/fixtures/fake-github.py, a
// loopback server that answers from fixtures and records what it was asked;
// the gh adapter sends to that URL the same way it sends to api.github.com.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/zetlen/falconet/internal/forge"
)

// GH is a Client backed by the `gh` CLI. It shells out to `gh api` for every
// call, sending full URLs so the fake-github.py test server and a real
// GITHUB_API_URL are reached the same way. The token is passed explicitly via
// -H so that non-github.com hosts (the test server, GitHub Enterprise Server)
// are authenticated the same way github.com is. The verbs check TokenFromEnv
// before constructing a GH, so a missing token is a clear early error rather
// than a gh diagnostic mid-run.
type GH struct {
	baseURL string
	token   string
}

// NewGH creates a Client that shells out to `gh api` against baseURL,
// authenticating with token.
func NewGH(baseURL, token string) *GH {
	return &GH{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

// do makes one request through `gh api -i`. in, when not nil, is sent as
// JSON via --input; out, when not nil, is filled from the JSON response.
// Any HTTP status outside 2xx is a *forge.Error.
func (g *GH) do(method, path string, in, out any) error {
	fullURL := g.baseURL + path
	args := []string{"api", "-i", fullURL}
	if g.token != "" {
		args = append(args, "-H", "Authorization: Bearer "+g.token)
	}
	if method != "GET" {
		args = append(args, "-X", method)
	}

	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%s %s: encoding the request: %v", method, path, err)
		}
		f, err := os.CreateTemp("", "falconet-*.json")
		if err != nil {
			return fmt.Errorf("%s %s: creating request body: %v", method, path, err)
		}
		defer func() { _ = os.Remove(f.Name()) }()
		if _, err := f.Write(raw); err != nil {
			_ = f.Close()
			return fmt.Errorf("%s %s: writing request body: %v", method, path, err)
		}
		_ = f.Close()
		args = append(args, "--input", f.Name())
	}

	cmd := exec.Command("gh", args...)
	// The header above is what authenticates the request, on every host.
	// GH_TOKEN is what lets gh start at all: inside GitHub Actions, gh
	// refuses to run with no token in its environment, whatever the
	// request carries, and the token a verb resolved is not necessarily
	// in the environment gh inherits.
	if g.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+g.token)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()

	output := stdout.Bytes()
	if len(output) == 0 {
		return fmt.Errorf("%s %s: gh produced no output: %s", method, path, strings.TrimSpace(stderr.String()))
	}

	status, body := parseResponse(output)
	if status == 0 {
		return fmt.Errorf("%s %s: could not parse HTTP status from gh output", method, path)
	}
	if status < 200 || status > 299 {
		return &forge.Error{Method: method, Path: path, Status: status, Message: forge.Message(body)}
	}

	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("%s %s: decoding the response: %v", method, path, err)
		}
	}
	return nil
}

// parseResponse splits `gh api -i` output into the HTTP status code and the
// response body. The format is the status line, headers, a blank line, then
// the body:
//
//	HTTP/2.0 200 OK\r\n
//	Content-Type: application/json\r\n
//	\r\n
//	{"key":"value"}
func parseResponse(output []byte) (int, []byte) {
	// The status line ends at the first \r or \n; if there is none, the
	// entire output is the status line (no headers, no body).
	statusLine := string(output)
	if end := bytes.IndexAny(output, "\r\n"); end >= 0 {
		statusLine = string(output[:end])
	}
	parts := strings.SplitN(statusLine, " ", 3)
	status := 0
	if len(parts) >= 2 {
		status, _ = strconv.Atoi(parts[1])
	}

	var body []byte
	if i := bytes.Index(output, []byte("\r\n\r\n")); i >= 0 {
		body = output[i+4:]
	} else if i := bytes.Index(output, []byte("\n\n")); i >= 0 {
		body = output[i+2:]
	}
	return status, body
}

// --- reads -----------------------------------------------------------------

func (g *GH) GetIssue(owner, name string, number int) (*forge.Issue, error) {
	var out forge.Issue
	if err := g.do("GET", forge.IssuePath(owner, name, number, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (g *GH) GetIssueRaw(owner, name string, number int) (json.RawMessage, error) {
	var out json.RawMessage
	if err := g.do("GET", forge.IssuePath(owner, name, number, ""), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GH) ListIssueComments(owner, name string, number int) ([]forge.IssueComment, error) {
	var out []forge.IssueComment
	if err := g.do("GET", forge.IssuePath(owner, name, number, "comments?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GH) ListIssueCommentsRaw(owner, name string, number int) (json.RawMessage, error) {
	var out json.RawMessage
	if err := g.do("GET", forge.IssuePath(owner, name, number, "comments?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListOpenPulls is GET /repos/{owner}/{name}/pulls?state=open, one page of
// 100 — the 101st open pull request is not read.
func (g *GH) ListOpenPulls(owner, name string) ([]forge.PullRequest, error) {
	var out []forge.PullRequest
	if err := g.do("GET", forge.RepoPath(owner, name, "/pulls?state=open&per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GH) GetAuthenticatedUser() (*forge.User, error) {
	var out forge.User
	if err := g.do("GET", "/user", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RepoPermission is GET /repos/{owner}/{name}/collaborators/{login}/permission.
// GitHub's `permission` is the legacy base role over every grant: maintain
// answers write, triage answers read, a custom role answers its base.
// role_name and user.permissions are not read. A 404 is an error like any
// other non-2xx: GitHub answers it for a login that is no user AND for a
// token that cannot see the repository, and the second must not read as
// "nobody may start a run".
func (g *GH) RepoPermission(owner, name, login string) (forge.Permission, error) {
	if !forge.IsLoginWord(login) || login == "." || login == ".." {
		return "", fmt.Errorf("%q is not a login", login)
	}
	path := forge.RepoPath(owner, name, "/collaborators/"+url.PathEscape(login)+"/permission")
	var out struct {
		Permission *string `json:"permission"`
	}
	if err := g.do("GET", path, nil, &out); err != nil {
		return "", err
	}
	word := ""
	if out.Permission != nil {
		word = *out.Permission
		switch forge.Permission(word) {
		case forge.PermissionAdmin, forge.PermissionWrite, forge.PermissionRead, forge.PermissionNone:
			return forge.Permission(word), nil
		}
	}
	return "", fmt.Errorf("GET %s: answered permission %q, which is none of admin, write, read, none", path, word)
}

// --- writes ----------------------------------------------------------------

func (g *GH) CreateIssueComment(owner, name string, number int, body string) error {
	return g.do("POST", forge.IssuePath(owner, name, number, "comments"),
		map[string]string{"body": body}, nil)
}

func (g *GH) AddIssueLabels(owner, name string, number int, labels []string) error {
	return g.do("POST", forge.IssuePath(owner, name, number, "labels"),
		map[string][]string{"labels": labels}, nil)
}

func (g *GH) RemoveIssueLabel(owner, name string, number int, label string) error {
	return g.do("DELETE", forge.IssuePath(owner, name, number, "labels/"+url.PathEscape(label)), nil, nil)
}

func (g *GH) AddIssueAssignees(owner, name string, number int, logins []string) error {
	return g.do("POST", forge.IssuePath(owner, name, number, "assignees"),
		map[string][]string{"assignees": logins}, nil)
}

func (g *GH) RemoveIssueAssignees(owner, name string, number int, logins []string) error {
	return g.do("DELETE", forge.IssuePath(owner, name, number, "assignees"),
		map[string][]string{"assignees": logins}, nil)
}
