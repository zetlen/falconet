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
// Any HTTP status outside 2xx is an *Error.
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
		return &Error{Method: method, Path: path, Status: status, Message: Message(body)}
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

func (g *GH) GetIssue(owner, name string, number int) (*Issue, error) {
	var out Issue
	if err := g.do("GET", IssuePath(owner, name, number, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (g *GH) GetIssueRaw(owner, name string, number int) (json.RawMessage, error) {
	var out json.RawMessage
	if err := g.do("GET", IssuePath(owner, name, number, ""), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GH) ListIssueComments(owner, name string, number int) ([]IssueComment, error) {
	var out []IssueComment
	if err := g.do("GET", IssuePath(owner, name, number, "comments?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GH) ListIssueCommentsRaw(owner, name string, number int) (json.RawMessage, error) {
	var out json.RawMessage
	if err := g.do("GET", IssuePath(owner, name, number, "comments?per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListOpenPulls is GET /repos/{owner}/{name}/pulls?state=open, one page of
// 100 — the 101st open pull request is not read.
func (g *GH) ListOpenPulls(owner, name string) ([]PullRequest, error) {
	var out []PullRequest
	if err := g.do("GET", RepoPath(owner, name, "/pulls?state=open&per_page=100"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GH) GetAuthenticatedUser() (*User, error) {
	var out User
	if err := g.do("GET", "/user", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- writes ----------------------------------------------------------------

func (g *GH) CreateIssueComment(owner, name string, number int, body string) error {
	return g.do("POST", IssuePath(owner, name, number, "comments"),
		map[string]string{"body": body}, nil)
}

func (g *GH) AddIssueLabels(owner, name string, number int, labels []string) error {
	return g.do("POST", IssuePath(owner, name, number, "labels"),
		map[string][]string{"labels": labels}, nil)
}

func (g *GH) RemoveIssueLabel(owner, name string, number int, label string) error {
	return g.do("DELETE", IssuePath(owner, name, number, "labels/"+url.PathEscape(label)), nil, nil)
}

func (g *GH) AddIssueAssignees(owner, name string, number int, logins []string) error {
	return g.do("POST", IssuePath(owner, name, number, "assignees"),
		map[string][]string{"assignees": logins}, nil)
}

func (g *GH) RemoveIssueAssignees(owner, name string, number int, logins []string) error {
	return g.do("DELETE", IssuePath(owner, name, number, "assignees"),
		map[string][]string{"assignees": logins}, nil)
}
