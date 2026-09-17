package forge

import "testing"

// --- path helpers ------------------------------------------------------------

func TestIssuePathEscapesOwnerAndName(t *testing.T) {
	got := IssuePath("o", "a b", 42, "comments")
	want := "/repos/o/a%20b/issues/42/comments"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIssuePathNoRest(t *testing.T) {
	got := IssuePath("o", "r", 42, "")
	want := "/repos/o/r/issues/42"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoPath(t *testing.T) {
	got := RepoPath("o", "r", "/pulls?state=open")
	want := "/repos/o/r/pulls?state=open"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- utility functions -------------------------------------------------------

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
		{"github.com:zetlen/falconet@v1", "github.com", "", "", false},
		{"host:a/b@c", "github.com", "", "", false},
		{"git@github.com:zetlen/falconet@v1", "github.com", "", "", false},
		{"git@github.com:zetlen/wayfinders-infra", "github.com", "zetlen", "wayfinders-infra", true},
		{"ssh://git@github.com/zetlen/wayfinders-infra.git", "github.com", "zetlen", "wayfinders-infra", true},
		{"ssh://git@github.com:22/o/r", "github.com", "o", "r", true},
		{"https://github.example.com/o/r.git", "github.example.com", "o", "r", true},
		{"git@github.example.com:o/r.git", "github.example.com", "o", "r", true},
		{"  https://github.com/o/r\n", "github.com", "o", "r", true},
		{"https://gitlab.com/o/r.git", "github.com", "", "", false},
		{"git@gitlab.com:o/r.git", "github.com", "", "", false},
		{"https://github.com/o/r", "github.example.com", "", "", false},
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

// --- Error and Message -------------------------------------------------------

func TestAnErrorSaysTheMethodPathStatusAndMessage(t *testing.T) {
	e := &Error{Method: "POST", Path: "/repos/o/r/issues/36/labels", Status: 422, Message: "Validation Failed"}
	if got := e.Error(); got != "POST /repos/o/r/issues/36/labels: 422 Validation Failed" {
		t.Errorf("Error(): %q", got)
	}
}

func TestA404SaysNotFoundOrNoAccess(t *testing.T) {
	for _, msg := range []string{"Not Found", ""} {
		e := &Error{Method: "POST", Path: "/repos/o/private/issues/1/comments", Status: 404, Message: msg}
		if got := e.Error(); got != "POST /repos/o/private/issues/1/comments: 404 not found, or no access" {
			t.Errorf("message %q: Error(): %q", msg, got)
		}
	}
}

func TestAnErrorWithNoMessageUsesTheStatusText(t *testing.T) {
	e := &Error{Method: "POST", Path: "/repos/o/r/issues/1/comments", Status: 502}
	if got := e.Error(); got != "POST /repos/o/r/issues/1/comments: 502 Bad Gateway" {
		t.Errorf("Error(): %q", got)
	}
}

func TestMessageReadsTheMessageFieldOrNothing(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"message":"Validation Failed","errors":[]}`, "Validation Failed"},
		{`{"errors":[]}`, ""},
		{`{"message":7}`, ""},
		{`<html>bad gateway</html>`, ""},
		{``, ""},
	} {
		if got := Message([]byte(tc.raw)); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.raw, got, tc.want)
		}
	}
}
