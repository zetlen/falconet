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
