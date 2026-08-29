package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// recorded is one request as the test server saw it.
type recorded struct {
	Method string
	Path   string
	Header http.Header
	Body   map[string]any
}

// serve is a GitHub that answers every request with status and body, and
// hands back what it was asked.
func serve(t *testing.T, status int, body string) (*Client, *[]recorded) {
	t.Helper()
	var seen []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &parsed)
		}
		seen = append(seen, recorded{r.Method, r.URL.Path, r.Header.Clone(), parsed})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "tok-123"), &seen
}

func TestTheThreeWritesAreShapedLikeGitHubs(t *testing.T) {
	c, seen := serve(t, 200, `{}`)
	if err := c.CreateIssueComment("o", "r", 36, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddIssueLabels("o", "r", 36, []string{"ready-for-human"}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveIssueAssignees("o", "r", 36, []string{"zetlen"}); err != nil {
		t.Fatal(err)
	}
	want := []recorded{
		{"POST", "/repos/o/r/issues/36/comments", nil, map[string]any{"body": "hello"}},
		{"POST", "/repos/o/r/issues/36/labels", nil, map[string]any{"labels": []any{"ready-for-human"}}},
		{"DELETE", "/repos/o/r/issues/36/assignees", nil, map[string]any{"assignees": []any{"zetlen"}}},
	}
	if len(*seen) != len(want) {
		t.Fatalf("saw %d requests, want %d", len(*seen), len(want))
	}
	for i, got := range *seen {
		if got.Method != want[i].Method || got.Path != want[i].Path || !reflect.DeepEqual(got.Body, want[i].Body) {
			t.Errorf("request %d: got %s %s %v, want %s %s %v",
				i, got.Method, got.Path, got.Body, want[i].Method, want[i].Path, want[i].Body)
		}
	}
}

func TestEveryRequestCarriesTheTokenAndTheHeadersGitHubAsksFor(t *testing.T) {
	c, seen := serve(t, 201, `{}`)
	if err := c.CreateIssueComment("o", "r", 1, "x"); err != nil {
		t.Fatal(err)
	}
	h := (*seen)[0].Header
	for name, want := range map[string]string{
		"Authorization":        "Bearer tok-123",
		"Accept":               "application/vnd.github+json",
		"X-Github-Api-Version": "2022-11-28",
		"Content-Type":         "application/json",
		"User-Agent":           "falconet",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestOwnerAndNameAreEscapedIntoThePath(t *testing.T) {
	c, seen := serve(t, 200, `{}`)
	if err := c.AddIssueLabels("o", "a b", 1, nil); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0].Path; got != "/repos/o/a b/issues/1/labels" {
		t.Errorf("path decoded to %q", got)
	}
}

func TestAnErrorCarriesTheStatusAndGitHubsMessage(t *testing.T) {
	c, _ := serve(t, 422, `{"message":"Validation Failed","errors":[]}`)
	err := c.AddIssueLabels("o", "r", 36, []string{"nope"})
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("got %T %v, want *Error", err, err)
	}
	if e.Status != 422 || e.Message != "Validation Failed" || e.Method != "POST" ||
		e.Path != "/repos/o/r/issues/36/labels" {
		t.Errorf("got %+v", *e)
	}
	if got := err.Error(); got != "POST /repos/o/r/issues/36/labels: 422 Validation Failed" {
		t.Errorf("Error(): %q", got)
	}
}

func TestA404SaysNotFoundOrNoAccess(t *testing.T) {
	c, _ := serve(t, 404, `{"message":"Not Found"}`)
	err := c.CreateIssueComment("o", "private", 1, "x")
	if err == nil || !strings.Contains(err.Error(), "404 not found, or no access") {
		t.Errorf("got %v", err)
	}
}

func TestAnErrorWithNoMessageUsesTheStatusText(t *testing.T) {
	c, _ := serve(t, 502, `<html>bad gateway</html>`)
	err := c.CreateIssueComment("o", "r", 1, "x")
	if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Errorf("got %v", err)
	}
}

func TestAResponseIsDecodedWhenAsked(t *testing.T) {
	c, _ := serve(t, 200, `{"id": 7, "name": "x"}`)
	var out struct {
		ID int `json:"id"`
	}
	if err := c.Do("GET", "/anything", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != 7 {
		t.Errorf("id %d", out.ID)
	}
}

func TestAnUnreachableEndpointIsAnErrorNotAPanic(t *testing.T) {
	c := New("http://127.0.0.1:1", "t")
	if err := c.CreateIssueComment("o", "r", 1, "x"); err == nil {
		t.Error("expected an error")
	}
}

func TestSplitRepository(t *testing.T) {
	for _, tc := range []struct {
		in, owner, name string
		ok              bool
	}{
		{"zetlen/wayfinders-infra", "zetlen", "wayfinders-infra", true},
		{"o/r", "o", "r", true},
		{"", "", "", false},
		{"noslash", "", "", false},
		{"/r", "", "", false},
		{"o/", "", "", false},
		{"o/r/extra", "", "", false},
	} {
		owner, name, err := SplitRepository(tc.in)
		if (err == nil) != tc.ok || owner != tc.owner || name != tc.name {
			t.Errorf("%q: got (%q, %q, %v), want (%q, %q, ok=%v)", tc.in, owner, name, err, tc.owner, tc.name, tc.ok)
		}
	}
}

func TestAPIURLFromEnv(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	if got := APIURLFromEnv(); got != DefaultAPIURL {
		t.Errorf("unset: %q", got)
	}
	t.Setenv("GITHUB_API_URL", "http://127.0.0.1:4321/")
	if got := APIURLFromEnv(); got != "http://127.0.0.1:4321" {
		t.Errorf("trailing slash kept: %q", got)
	}
}

func TestTokenFromEnvPrefersGHToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	if got := TokenFromEnv(); got != "" {
		t.Errorf("neither set: %q", got)
	}
	t.Setenv("GITHUB_TOKEN", "gt")
	if got := TokenFromEnv(); got != "gt" {
		t.Errorf("GITHUB_TOKEN alone: %q", got)
	}
	t.Setenv("GH_TOKEN", "gh")
	if got := TokenFromEnv(); got != "gh" {
		t.Errorf("both set: %q", got)
	}
}

// --- the reads, the next writes, and what each asks for ------------------------

// served is a GitHub that answers each path from a table and records what it
// was asked.
func served(t *testing.T, answers map[string]string) (*Client, *[]recorded) {
	t.Helper()
	var seen []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &parsed)
		}
		seen = append(seen, recorded{r.Method, r.URL.RequestURI(), r.Header.Clone(), parsed})
		body, ok := answers[r.Method+" "+r.URL.RequestURI()]
		if !ok {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "tok-123"), &seen
}

func TestTheReadsAskTheEndpointsTheyDocumentAndDecodeGitHubsShapes(t *testing.T) {
	c, seen := served(t, map[string]string{
		"GET /repos/o/r/issues/42":                       `{"number":42,"title":"Add MX","body":"please","state":"open","labels":[{"name":"infra-request","color":"ededed"}],"user":{"login":"zetlen","type":"User"},"assignees":[{"login":"bot","type":"Bot"}]}`,
		"GET /repos/o/r/issues/43":                       `{"number":43,"title":"a PR","pull_request":{"url":"https://api.github.invalid/repos/o/r/pulls/43"}}`,
		"GET /repos/o/r/issues/42/comments?per_page=100": `[{"user":{"login":"zetlen"},"created_at":"2026-08-01T00:00:00Z","body":"bump"}]`,
		"GET /repos/o/r/pulls?state=open&per_page=100":   `[{"number":7,"head":{"ref":"issue-42-add-mx"}}]`,
		"GET /user": `{"login":"fake-user","type":"User"}`,
	})

	issue, err := c.GetIssue("o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 42 || issue.Title != "Add MX" || issue.Body != "please" || issue.State != "open" ||
		len(issue.Labels) != 1 || issue.Labels[0].Name != "infra-request" ||
		issue.User.Login != "zetlen" || len(issue.Assignees) != 1 || issue.Assignees[0].Login != "bot" ||
		issue.PullRequest != nil {
		t.Errorf("issue: %+v", *issue)
	}
	pr, err := c.GetIssue("o", "r", 43)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PullRequest == nil || pr.PullRequest.URL == "" {
		t.Errorf("an issue that is a pull request says so: %+v", *pr)
	}
	comments, err := c.ListIssueComments("o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].User.Login != "zetlen" || comments[0].CreatedAt != "2026-08-01T00:00:00Z" || comments[0].Body != "bump" {
		t.Errorf("comments: %+v", comments)
	}
	pulls, err := c.ListOpenPulls("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 1 || pulls[0].Number != 7 || pulls[0].Head.Ref != "issue-42-add-mx" {
		t.Errorf("pulls: %+v", pulls)
	}
	user, err := c.GetAuthenticatedUser()
	if err != nil {
		t.Fatal(err)
	}
	if user.Login != "fake-user" || user.Type != "User" {
		t.Errorf("user: %+v", *user)
	}
	want := []string{
		"GET /repos/o/r/issues/42",
		"GET /repos/o/r/issues/43",
		"GET /repos/o/r/issues/42/comments?per_page=100",
		"GET /repos/o/r/pulls?state=open&per_page=100",
		"GET /user",
	}
	var got []string
	for _, r := range *seen {
		got = append(got, r.Method+" "+r.Path)
		if r.Body != nil {
			t.Errorf("%s %s sent a body: %v", r.Method, r.Path, r.Body)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests:\n got %q\nwant %q", got, want)
	}
}

func TestTheNextWritesAreShapedLikeGitHubs(t *testing.T) {
	c, seen := serve(t, 200, `{}`)
	if err := c.RemoveIssueLabel("o", "r", 36, "needs info"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddIssueAssignees("o", "r", 36, []string{"falconet[bot]"}); err != nil {
		t.Fatal(err)
	}
	want := []recorded{
		{"DELETE", "/repos/o/r/issues/36/labels/needs%20info", nil, nil},
		{"POST", "/repos/o/r/issues/36/assignees", nil, map[string]any{"assignees": []any{"falconet[bot]"}}},
	}
	if len(*seen) != len(want) {
		t.Fatalf("saw %d requests, want %d", len(*seen), len(want))
	}
	for i, got := range *seen {
		// serve records the decoded path; the label's escaping is checked on
		// the raw one below.
		if got.Method != want[i].Method || !reflect.DeepEqual(got.Body, want[i].Body) {
			t.Errorf("request %d: got %s %s %v, want %s %s %v",
				i, got.Method, got.Path, got.Body, want[i].Method, want[i].Path, want[i].Body)
		}
	}
	if got := (*seen)[0].Path; got != "/repos/o/r/issues/36/labels/needs info" {
		t.Errorf("label path decoded to %q", got)
	}
}

func TestServerHostFromEnv(t *testing.T) {
	t.Setenv("GITHUB_SERVER_URL", "")
	if got := ServerHostFromEnv(); got != "github.com" {
		t.Errorf("unset: %q", got)
	}
	t.Setenv("GITHUB_SERVER_URL", "https://github.example.com")
	if got := ServerHostFromEnv(); got != "github.example.com" {
		t.Errorf("enterprise: %q", got)
	}
	t.Setenv("GITHUB_SERVER_URL", "https://GitHub.com/")
	if got := ServerHostFromEnv(); got != "GitHub.com" {
		t.Errorf("trailing slash: %q", got)
	}
	t.Setenv("GITHUB_SERVER_URL", "not a url")
	if got := ServerHostFromEnv(); got != "github.com" {
		t.Errorf("garbage: %q", got)
	}
}

func TestParseRemoteURL(t *testing.T) {
	for _, tc := range []struct {
		remote, host, owner, name string
		ok                        bool
	}{
		{"https://github.com/zetlen/wayfinders-infra", "github.com", "zetlen", "wayfinders-infra", true},
		{"https://github.com/zetlen/wayfinders-infra.git", "github.com", "zetlen", "wayfinders-infra", true},
		{"https://github.com/zetlen/wayfinders-infra/", "github.com", "zetlen", "wayfinders-infra", true},
		{"https://github.com/zetlen/wayfinders-infra.git/", "github.com", "zetlen", "wayfinders-infra", true},
		{"https://x-access-token:ghs_abc@github.com/o/r.git", "github.com", "o", "r", true},
		{"https://GitHub.com/o/r", "github.com", "o", "r", true},
		{"git@github.com:zetlen/wayfinders-infra.git", "github.com", "zetlen", "wayfinders-infra", true},
		// An '@' after the colon is part of the path, not a user; these
		// once sliced past the colon and panicked.
		{"github.com:zetlen/falconet@v1", "github.com", "", "", false},
		{"host:a/b@c", "github.com", "", "", false},
		{"git@github.com:zetlen/falconet@v1", "github.com", "", "", false},
		{"git@github.com:zetlen/wayfinders-infra", "github.com", "zetlen", "wayfinders-infra", true},
		{"ssh://git@github.com/zetlen/wayfinders-infra.git", "github.com", "zetlen", "wayfinders-infra", true},
		{"ssh://git@github.com:22/o/r", "github.com", "o", "r", true},
		{"https://github.example.com/o/r.git", "github.example.com", "o", "r", true},
		{"git@github.example.com:o/r.git", "github.example.com", "o", "r", true},
		{"  https://github.com/o/r\n", "github.com", "o", "r", true},
		// Not GitHub, or not this GitHub.
		{"https://gitlab.com/o/r.git", "github.com", "", "", false},
		{"git@gitlab.com:o/r.git", "github.com", "", "", false},
		{"https://github.com/o/r", "github.example.com", "", "", false},
		// Not a repository.
		{"https://github.com/o", "github.com", "", "", false},
		{"https://github.com/o/r/extra", "github.com", "", "", false},
		{"https://github.com/", "github.com", "", "", false},
		{"git@github.com:o", "github.com", "", "", false},
		{"/home/me/repos/r", "github.com", "", "", false},
		{"../r.git", "github.com", "", "", false},
		{"", "github.com", "", "", false},
		{"https://github.com:abc/o/r", "github.com", "", "", false},
	} {
		owner, name, err := ParseRemoteURL(tc.remote, tc.host)
		if (err == nil) != tc.ok || owner != tc.owner || name != tc.name {
			t.Errorf("%q on %s: got (%q, %q, %v), want (%q, %q, ok=%v)",
				tc.remote, tc.host, owner, name, err, tc.owner, tc.name, tc.ok)
		}
	}
}

// The raw reads hand back exactly the bytes GitHub sent — every field, in
// GitHub's order — and ask the same endpoints the typed reads do.
func TestTheRawReadsAreTheBytesGitHubSent(t *testing.T) {
	issue := `{"number":42,"title":"Add MX","body":null,"state":"open","labels":[{"name":"infra-request","color":"ededed"}],"reactions":{"+1":3},"node_id":"I_1"}`
	comments := `[{"id":9,"user":{"login":"zetlen","type":"User"},"created_at":"2026-08-01T00:00:00Z","body":"bump","reactions":{}}]`
	c, seen := served(t, map[string]string{
		"GET /repos/o/r/issues/42":                       issue,
		"GET /repos/o/r/issues/42/comments?per_page=100": comments,
	})
	rawIssue, err := c.GetIssueRaw("o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if string(rawIssue) != issue {
		t.Errorf("GetIssueRaw = %s, want the bytes served", rawIssue)
	}
	rawComments, err := c.ListIssueCommentsRaw("o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if string(rawComments) != comments {
		t.Errorf("ListIssueCommentsRaw = %s, want the bytes served", rawComments)
	}
	// The typed views decode from those same bytes.
	var typed Issue
	if err := json.Unmarshal(rawIssue, &typed); err != nil || typed.Number != 42 || typed.Title != "Add MX" || typed.Body != "" {
		t.Errorf("the typed issue does not decode from the raw one: %v %+v", err, typed)
	}
	var thread []IssueComment
	if err := json.Unmarshal(rawComments, &thread); err != nil || len(thread) != 1 || thread[0].User.Login != "zetlen" {
		t.Errorf("the typed comments do not decode from the raw ones: %v %+v", err, thread)
	}
	want := []string{"GET /repos/o/r/issues/42", "GET /repos/o/r/issues/42/comments?per_page=100"}
	var got []string
	for _, r := range *seen {
		got = append(got, r.Method+" "+r.Path)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests: got %q, want %q", got, want)
	}
	// A refusal is the same *Error the typed reads return.
	if _, err := c.GetIssueRaw("o", "r", 43); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404 on the raw read: %v", err)
	}
}
