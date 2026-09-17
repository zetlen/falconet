package gitea

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zetlen/falconet/internal/forge"
)

// --- a Gitea-shaped server on loopback -------------------------------------
//
// The bodies are Gitea 1.26's shapes as its swagger.json defines them (Issue,
// Comment, Label, PullRequest, RepoCollaboratorPermission, APIError), with
// the fields a verb does not read left in, so the adapter is shown decoding
// what Gitea sends rather than what GitHub sends.

const (
	token = "gitea-test-token-7f3a"
	bot   = "falconet-bot"
)

const notFound = `{"message":"not found","url":"https://got.example/api/swagger","errors":null}`

func userJSON(login string) string {
	return fmt.Sprintf(`{"id":3,"login":%q,"login_name":"","source_id":0,"full_name":"","email":"%s@noreply.got.example","avatar_url":"","html_url":"https://got.example/%s","language":"","is_admin":false,"last_login":"2026-09-01T00:00:00Z","created":"2026-01-01T00:00:00Z","restricted":false,"active":true,"prohibit_login":false,"location":"","website":"","description":"","visibility":"public","followers_count":0,"following_count":0,"starred_repos_count":0}`, login, login, login)
}

func labelJSON(id int, name string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"exclusive":false,"is_archived":false,"color":"ededed","description":"","url":"https://got.example/api/v1/repos/o/r/labels/%d"}`, id, name, id)
}

func issueJSON(number int, assignees []string, pull bool) string {
	as := "null"
	if assignees != nil {
		parts := []string{}
		for _, a := range assignees {
			parts = append(parts, userJSON(a))
		}
		as = "[" + strings.Join(parts, ",") + "]"
	}
	pr := "null"
	if pull {
		pr = fmt.Sprintf(`{"draft":false,"html_url":"https://got.example/o/r/pulls/%d","merged":false,"merged_at":null}`, number)
	}
	return fmt.Sprintf(`{"id":90,"url":"https://got.example/api/v1/repos/o/r/issues/%d","html_url":"https://got.example/o/r/issues/%d","number":%d,"user":%s,"original_author":"","original_author_id":0,"title":"Add MX","body":"please","ref":"","assets":[],"labels":[%s],"milestone":null,"assignee":null,"assignees":%s,"state":"open","is_locked":false,"comments":1,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z","closed_at":null,"due_date":null,"time_estimate":0,"pull_request":%s,"repository":{"id":1,"name":"r","owner":"o","full_name":"o/r"},"pin_order":0,"content_version":1}`,
		number, number, number, userJSON("zetlen"), labelJSON(1, "falconet"), as, pr)
}

// issueWithLabels is issueJSON with labels in place of the falconet label.
func issueWithLabels(number int, labels ...string) string {
	return strings.Replace(issueJSON(number, nil, false),
		`"labels":[`+labelJSON(1, "falconet")+`]`, `"labels":[`+strings.Join(labels, ",")+`]`, 1)
}

func pullJSON(number int, ref string) string {
	return fmt.Sprintf(`{"id":%d,"url":"https://got.example/o/r/pulls/%d","number":%d,"user":%s,"title":"t","body":"b","labels":[],"state":"open","draft":false,"head":{"label":%q,"ref":%q,"sha":"0123456789abcdef0123456789abcdef01234567","repo_id":1},"base":{"label":"main","ref":"main","sha":"89abcdef0123456789abcdef0123456789abcdef","repo_id":1}}`,
		500+number, number, number, userJSON(bot), ref, ref)
}

// rec is one request as the server saw it, with the /api/v1 prefix removed.
type rec struct {
	Method string
	Path   string // escaped path, no query
	Query  string
	Auth   string
	Body   string
}

// answer is what a route sends back.
type answer func(r *http.Request, body []byte) (int, string)

func ok(body string) answer {
	return func(*http.Request, []byte) (int, string) { return 200, body }
}

func status(code int, body string) answer {
	return func(*http.Request, []byte) (int, string) { return code, body }
}

// server is a loopback Gitea answering "METHOD /path" from routes, and 404
// with Gitea's not-found body for anything else. GET /user answers the bot
// unless routes says otherwise.
type server struct {
	URL string // the API base, …/api/v1
	mu  sync.Mutex
	got []rec
}

func (s *server) seen() []rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rec(nil), s.got...)
}

func serve(t *testing.T, routes map[string]answer) *server {
	t.Helper()
	s := &server{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1")
		s.mu.Lock()
		s.got = append(s.got, rec{r.Method, path, r.URL.RawQuery, r.Header.Get("Authorization"), string(raw)})
		s.mu.Unlock()
		key := r.Method + " " + path
		a, found := routes[key]
		if !found && key == "GET /user" {
			a, found = ok(userJSON(bot)), true
		}
		code, body := 404, notFound
		if found {
			code, body = a(r, raw)
		}
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL + "/api/v1"
	return s
}

func client(t *testing.T, s *server) *Client {
	t.Helper()
	c, err := New(s.URL, token, bot)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// paged answers a list route from items, honouring `limit` up to cap and
// `page`, as gitea routers/api/v1/utils/page.go does: past the end is [].
func paged(items []string, cap int) answer {
	return func(r *http.Request, _ []byte) (int, string) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 30
		}
		if limit > cap {
			limit = cap
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		lo := (page - 1) * limit
		if lo >= len(items) {
			return 200, "[]"
		}
		hi := min(lo+limit, len(items))
		return 200, "[" + strings.Join(items[lo:hi], ",") + "]"
	}
}

func requests(s *server, method, path string) []rec {
	var out []rec
	for _, r := range s.seen() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// --- construction ------------------------------------------------------------

func TestNewRefusesPlaintextToARemoteHost(t *testing.T) {
	for _, tc := range []struct {
		url, token, bot string
		ok              bool
	}{
		{"https://got.example/api/v1", token, bot, true},
		{"https://got.example/api/v1/", token, bot, true},
		{"http://127.0.0.1:3000/api/v1", token, bot, true},
		{"http://[::1]:3000/api/v1", token, bot, true},
		{"http://localhost:3000/api/v1", token, bot, false},
		{"http://localhost.got.example/api/v1", token, bot, false},
		{"http://got.example/api/v1", token, bot, false},
		{"http://127.0.0.1.got.example/api/v1", token, bot, false},
		{"ftp://got.example/api/v1", token, bot, false},
		{"got.example/api/v1", token, bot, false},
		{"https://user:pw@got.example/api/v1", token, bot, false},
		{"https://got.example/api/v1?x=1", token, bot, false},
		{"https://got.example/api/v1?", token, bot, false},
		{"https://got.example/api/v1/?", token, bot, false},
		{"https://got.example/api/v1", "", bot, false},
		{"https://got.example/api/v1", token, "", false},
		{"https://got.example/api/v1", token, "a/b", false},
		{"https://got.example/api/v1", token, "..", false},
	} {
		_, err := New(tc.url, tc.token, tc.bot)
		if (err == nil) != tc.ok {
			t.Errorf("New(%q, %q, %q): err %v, want ok=%v", tc.url, tc.token, tc.bot, err, tc.ok)
		}
		if err != nil && tc.token != "" && strings.Contains(err.Error(), tc.token) {
			t.Errorf("New(%q): the error carries the token: %v", tc.url, err)
		}
	}
}

// --- the self-check ------------------------------------------------------------

func TestTheFirstCallChecksTheTokenIsTheBot(t *testing.T) {
	s := serve(t, map[string]answer{
		"GET /user":               ok(userJSON("someone")),
		"GET /repos/o/r/issues/7": ok(issueJSON(7, nil, false)),
	})
	c := client(t, s)
	_, err := c.GetIssue("o", "r", 7)
	if err == nil || !strings.Contains(err.Error(), "someone") || !strings.Contains(err.Error(), bot) {
		t.Fatalf("got %v, want an error naming someone and %s", err, bot)
	}
	if err := c.CreateIssueComment("o", "r", 7, "hello"); err == nil {
		t.Fatal("a second call went through after the self-check failed")
	}
	if _, err := c.GetAuthenticatedUser(); err == nil {
		t.Fatal("GetAuthenticatedUser answered after the self-check failed")
	}
	if got := s.seen(); len(got) != 1 || got[0].Path != "/user" {
		t.Fatalf("requests: %+v, want exactly GET /user", got)
	}
}

func TestAFailedSelfCheckSendsNothingFurther(t *testing.T) {
	s := serve(t, map[string]answer{
		"GET /user":               status(500, `{"message":"database is locked","url":"https://got.example/api/swagger"}`),
		"GET /repos/o/r/issues/7": ok(issueJSON(7, nil, false)),
	})
	c := client(t, s)
	if _, err := c.GetIssue("o", "r", 7); err == nil {
		t.Fatal("GetIssue answered after GET /user failed")
	}
	if err := c.CreateIssueComment("o", "r", 7, "hello"); err == nil {
		t.Fatal("a second call went through after GET /user failed")
	}
	if got := s.seen(); len(got) != 1 || got[0].Path != "/user" {
		t.Fatalf("requests: %+v, want exactly one GET /user", got)
	}
}

func TestTheSelfCheckIsCaseInsensitiveAndOnce(t *testing.T) {
	s := serve(t, map[string]answer{
		"GET /user":               ok(userJSON("Falconet-Bot")),
		"GET /repos/o/r/issues/7": ok(issueJSON(7, nil, false)),
	})
	c := client(t, s)
	for range 2 {
		if _, err := c.GetIssue("o", "r", 7); err != nil {
			t.Fatal(err)
		}
	}
	u, err := c.GetAuthenticatedUser()
	if err != nil || u.Login != "Falconet-Bot" {
		t.Fatalf("GetAuthenticatedUser: %+v %v", u, err)
	}
	if n := len(requests(s, "GET", "/user")); n != 1 {
		t.Fatalf("GET /user sent %d times, want 1", n)
	}
	if n := len(s.seen()); n != 3 {
		t.Fatalf("sent %d requests, want 3", n)
	}
}

// --- transport ---------------------------------------------------------------

func TestARedirectIsNeverFollowed(t *testing.T) {
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = append(elsewhere, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, userJSON(bot))
	}))
	t.Cleanup(other.Close)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", other.URL+r.URL.Path)
		w.WriteHeader(http.StatusSeeOther)
	}))
	t.Cleanup(front.Close)
	c, err := New(front.URL+"/api/v1", token, bot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetIssue("o", "r", 1)
	var e *forge.Error
	if !errors.As(err, &e) || e.Status != http.StatusSeeOther {
		t.Fatalf("got %T %v, want *forge.Error with 303", err, err)
	}
	if len(elsewhere) != 0 {
		t.Fatalf("the redirect was followed: %v", elsewhere)
	}
}

func TestAnErrorCarriesGiteasMessage(t *testing.T) {
	s := serve(t, map[string]answer{
		"POST /repos/o/r/issues/7/comments": status(423, `{"message":"repository is archived","url":"https://got.example/api/swagger"}`),
	})
	c := client(t, s)
	err := c.CreateIssueComment("o", "r", 7, "hello")
	var e *forge.Error
	if !errors.As(err, &e) {
		t.Fatalf("got %T %v, want *forge.Error", err, err)
	}
	if e.Status != 423 || e.Method != "POST" || e.Path != "/repos/o/r/issues/7/comments" || e.Message != "repository is archived" {
		t.Errorf("got %+v", *e)
	}

	_, err = c.GetIssue("o", "r", 8)
	if !errors.As(err, &e) || e.Status != 404 || !strings.Contains(err.Error(), "not found, or no access") {
		t.Errorf("a 404: got %v", err)
	}
}

func TestAnAnswerPastTheSizeLimitIsAnError(t *testing.T) {
	big := `{"number":7,"body":"` + strings.Repeat("x", maxBody) + `"}`
	s := serve(t, map[string]answer{"GET /repos/o/r/issues/7": ok(big)})
	c := client(t, s)
	if _, err := c.GetIssue("o", "r", 7); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v, want a size error", err)
	}
}

func TestAPathNeverCarriesAnotherPath(t *testing.T) {
	for _, repo := range [][2]string{{"..", "r"}, {"o", "."}, {"o/x", "r"}, {"o", "r?x"}} {
		s := serve(t, nil)
		errs := drive(t, client(t, s), repo[0], repo[1])
		// Every method but GetAuthenticatedUser names the repository.
		if len(errs) != 11 {
			t.Errorf("%s/%s: %d errors, want 11: %v", repo[0], repo[1], len(errs), errs)
		}
		for _, err := range errs {
			if !strings.Contains(err.Error(), "is not an owner/name repository") {
				t.Errorf("%s/%s: %v", repo[0], repo[1], err)
			}
		}
		for _, r := range s.seen() {
			if r.Path != "/user" {
				t.Errorf("sent %s %s", r.Method, r.Path)
			}
		}
	}

	s := serve(t, nil)
	c := client(t, s)
	for _, login := range []string{"..", "a/b", ""} {
		if _, err := c.RepoPermission("o", "r", login); err == nil {
			t.Errorf("RepoPermission(o, r, %q) answered", login)
		}
	}
	for _, r := range s.seen() {
		if r.Path != "/user" {
			t.Errorf("sent %s %s", r.Method, r.Path)
		}
	}
}

func TestARouteOffTheListIsNeverSent(t *testing.T) {
	s := serve(t, nil)
	c := client(t, s)
	for _, r := range []struct{ method, path string }{
		{"POST", "/repos/o/r/pulls/1/merge"},
		{"PUT", "/repos/o/r/issues/7/labels"},
		{"DELETE", "/repos/o/r/issues/7/labels"},
		{"GET", "/repos/o/r/labels"},
		{"DELETE", "/repos/o/r"},
		{"GET", "/repos/o/r/issues/7/labels/3/x"},
		{"GET", "/repos/o/r/issues/x"},
		{"POST", "/user"},
	} {
		err := c.send(r.method, r.path, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "not a route") {
			t.Errorf("%s %s: got %v, want a refusal", r.method, r.path, err)
		}
	}
	if got := s.seen(); len(got) != 0 {
		t.Fatalf("sent %+v", got)
	}
}

func TestAnAnswerWithNoDocumentIsAnError(t *testing.T) {
	for _, body := range []string{"null", "", " \n"} {
		s := serve(t, map[string]answer{
			"GET /repos/o/r/pulls":             ok(body),
			"GET /repos/o/r/issues/7/comments": ok(body),
		})
		c := client(t, s)
		if pulls, err := c.ListOpenPulls("o", "r"); err == nil {
			t.Errorf("pulls answered %q: got %v and no error", body, pulls)
		}
		if raw, err := c.ListIssueCommentsRaw("o", "r", 7); err == nil {
			t.Errorf("comments answered %q: got %s and no error", body, raw)
		}
	}
}

func TestAnErrorNeverCarriesAnEchoedToken(t *testing.T) {
	s := serve(t, map[string]answer{
		"GET /repos/o/r/issues/7": status(500, `{"message":"bad header: token `+token+strings.Repeat("y", 4000)+`"}`),
	})
	_, err := client(t, s).GetIssue("o", "r", 7)
	if err == nil {
		t.Fatal("a 500 answered nil")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the error carries the token: %.200s", err)
	}
	if len(err.Error()) > maxMessage+200 {
		t.Errorf("the error is %d bytes long", len(err.Error()))
	}
}

func TestAnIssueWithAnotherNumberIsRefused(t *testing.T) {
	// gitea services/convert/issue.go:37 answers {} with 200 when it cannot
	// load the issue's parts.
	s := serve(t, map[string]answer{
		"GET /repos/o/r/issues/7":   ok(`{"id":0,"number":0,"labels":null,"assignees":null}`),
		"PATCH /repos/o/r/issues/7": status(201, issueJSON(7, nil, false)),
	})
	c := client(t, s)
	if err := c.RemoveIssueAssignees("o", "r", 7, []string{"zetlen"}); err == nil {
		t.Error("RemoveIssueAssignees answered nil")
	}
	if err := c.AddIssueAssignees("o", "r", 7, []string{"zetlen"}); err == nil {
		t.Error("AddIssueAssignees answered nil")
	}
	if _, err := c.GetIssueRaw("o", "r", 7); err == nil {
		t.Error("GetIssueRaw answered nil")
	}
	if n := len(requests(s, "PATCH", "/repos/o/r/issues/7")); n != 0 {
		t.Errorf("patched %d times", n)
	}
}

// --- reads -------------------------------------------------------------------

func TestTheReadsDecodeGiteasShapes(t *testing.T) {
	s := serve(t, map[string]answer{
		"GET /repos/o/r/issues/42":          ok(issueJSON(42, nil, false)),
		"GET /repos/o/r/issues/43":          ok(issueJSON(43, []string{"zetlen"}, true)),
		"GET /repos/o/r/issues/42/comments": ok(`[{"id":1,"html_url":"https://got.example/o/r/issues/42#issuecomment-1","pull_request_url":"","issue_url":"https://got.example/o/r/issues/42","user":` + userJSON("zetlen") + `,"original_author":"","original_author_id":0,"body":"bump","assets":[],"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}]`),
		"GET /repos/o/r/pulls":              paged([]string{pullJSON(7, "issue-42-add-mx")}, 50),
	})
	c := client(t, s)
	issue, err := c.GetIssue("o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 42 || issue.Title != "Add MX" || issue.State != "open" || issue.User.Login != "zetlen" ||
		len(issue.Labels) != 1 || issue.Labels[0].Name != "falconet" || issue.PullRequest != nil || issue.Assignees != nil {
		t.Errorf("issue: %+v", issue)
	}
	pr, err := c.GetIssue("o", "r", 43)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PullRequest == nil || len(pr.Assignees) != 1 || pr.Assignees[0].Login != "zetlen" {
		t.Errorf("a pull request's issue: %+v", pr)
	}
	raw, err := c.GetIssueRaw("o", "r", 42)
	if err != nil || !json.Valid(raw) || !strings.Contains(string(raw), `"pin_order":0`) {
		t.Errorf("GetIssueRaw: %s %v", raw, err)
	}
	comments, err := c.ListIssueComments("o", "r", 42)
	if err != nil || len(comments) != 1 || comments[0].User.Login != "zetlen" || comments[0].Body != "bump" || comments[0].CreatedAt == "" {
		t.Errorf("ListIssueComments: %+v %v", comments, err)
	}
	rawComments, err := c.ListIssueCommentsRaw("o", "r", 42)
	if err != nil || !strings.Contains(string(rawComments), `"issue_url"`) {
		t.Errorf("ListIssueCommentsRaw: %s %v", rawComments, err)
	}
	pulls, err := c.ListOpenPulls("o", "r")
	if err != nil || len(pulls) != 1 || pulls[0].Number != 7 || pulls[0].Head.Ref != "issue-42-add-mx" {
		t.Errorf("ListOpenPulls: %+v %v", pulls, err)
	}
	for _, r := range requests(s, "GET", "/repos/o/r/pulls") {
		q := r.Query
		if !strings.Contains(q, "state=open") || !strings.Contains(q, "limit=50") || strings.Contains(q, "per_page") {
			t.Errorf("pulls query %q", q)
		}
	}
}

func TestListIssueCommentsSendsNoPaging(t *testing.T) {
	s := serve(t, map[string]answer{"GET /repos/o/r/issues/42/comments": ok(`[]`)})
	c := client(t, s)
	if _, err := c.ListIssueComments("o", "r", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListIssueCommentsRaw("o", "r", 42); err != nil {
		t.Fatal(err)
	}
	got := requests(s, "GET", "/repos/o/r/issues/42/comments")
	if len(got) != 2 {
		t.Fatalf("requests: %+v", s.seen())
	}
	for _, r := range got {
		if r.Query != "" {
			t.Errorf("comments query %q, want none", r.Query)
		}
	}
}

func TestPagingStopsOnAnEmptyPageNotAShortOne(t *testing.T) {
	var pulls []string
	for i := 1; i <= 45; i++ {
		pulls = append(pulls, pullJSON(i, fmt.Sprintf("issue-%d-x", i)))
	}
	// The instance caps every list at 30 though the adapter asks for 50, so
	// page 1 is short and not the last.
	s := serve(t, map[string]answer{
		"GET /repos/o/r/pulls": paged(pulls, 30),
	})
	c := client(t, s)
	got, err := c.ListOpenPulls("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 45 || got[44].Number != 45 {
		t.Fatalf("read %d pulls, want 45", len(got))
	}
	if n := len(requests(s, "GET", "/repos/o/r/pulls")); n != 3 {
		t.Errorf("pulls read in %d pages, want 3 (the last empty)", n)
	}
}

func TestPagingPastTheCapIsAnError(t *testing.T) {
	// A server that never answers an empty page.
	endless := func(item string) answer {
		return func(*http.Request, []byte) (int, string) { return 200, "[" + item + "]" }
	}
	s := serve(t, map[string]answer{
		"GET /repos/o/r/pulls": endless(pullJSON(1, "issue-1-x")),
	})
	c := client(t, s)
	if _, err := c.ListOpenPulls("o", "r"); err == nil {
		t.Error("ListOpenPulls answered from a list with no end")
	}
	if n := len(requests(s, "GET", "/repos/o/r/pulls")); n != maxPages+1 {
		t.Errorf("pulls pages read: %d, want %d", n, maxPages+1)
	}
}

func TestRepoPermissionMapsGiteasWords(t *testing.T) {
	perm := func(word string) answer {
		return ok(fmt.Sprintf(`{"permission":%q,"role_name":%q,"user":%s}`, word, word, userJSON("maint")))
	}
	for _, tc := range []struct {
		name string
		a    answer
		want forge.Permission
		ok   bool
	}{
		{"owner", perm("owner"), forge.PermissionAdmin, true},
		{"admin", perm("admin"), forge.PermissionAdmin, true},
		{"write", perm("write"), forge.PermissionWrite, true},
		{"read", perm("read"), forge.PermissionRead, true},
		{"none", perm("none"), forge.PermissionNone, true},
		{"maintain", perm("maintain"), "", false},
		{"Owner", perm("Owner"), "", false},
		{"empty", perm(""), "", false},
		{"missing", ok(`{"role_name":"write","user":` + userJSON("maint") + `}`), "", false},
		{"null", ok(`{"permission":null}`), "", false},
		{"404", status(404, notFound), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serve(t, map[string]answer{"GET /repos/o/r/collaborators/maint/permission": tc.a})
			got, err := client(t, s).RepoPermission("o", "r", "maint")
			if (err == nil) != tc.ok || got != tc.want {
				t.Errorf("got %q %v, want %q ok=%v", got, err, tc.want, tc.ok)
			}
		})
	}

	// gitea routers/api/v1/repo/collaborators.go:279-280
	s := serve(t, map[string]answer{
		"GET /repos/o/r/collaborators/maint/permission": status(403, `{"message":"Only admins can query all permissions, repo admins can query all repo permissions, collaborators can query only their own","url":"https://got.example/api/swagger"}`),
	})
	_, err := client(t, s).RepoPermission("o", "r", "maint")
	var e *forge.Error
	if !errors.As(err, &e) || e.Status != 403 || !strings.Contains(err.Error(), "must be an administrator of o/r") {
		t.Errorf("a 403: got %v, want a *forge.Error naming the administrator requirement", err)
	}
}

// --- labels ------------------------------------------------------------------

func TestAddIssueLabelsPostsNames(t *testing.T) {
	s := serve(t, map[string]answer{
		"POST /repos/o/r/issues/7/labels": func(_ *http.Request, body []byte) (int, string) {
			var in struct{ Labels []string }
			if err := json.Unmarshal(body, &in); err != nil {
				return 400, `{"message":"labels should be an array of strings or integers"}`
			}
			out := []string{labelJSON(101, "falconet")}
			for i, name := range in.Labels {
				out = append(out, labelJSON(200+i, name))
			}
			return 200, "[" + strings.Join(out, ",") + "]"
		},
	})
	c := client(t, s)
	if err := c.AddIssueLabels("o", "r", 7, []string{"ready-for-human", "area-3"}); err != nil {
		t.Fatal(err)
	}
	post := requests(s, "POST", "/repos/o/r/issues/7/labels")
	if len(post) != 1 || post[0].Body != `{"labels":["ready-for-human","area-3"]}` {
		t.Fatalf("posted %+v", post)
	}
}

func TestAddIssueLabelsRefusesAnAnswerMissingALabel(t *testing.T) {
	// Gitea answers 200 with the issue's labels, and a name that neither the
	// repository nor its organization defines is simply absent from them.
	s := serve(t, map[string]answer{
		"POST /repos/o/r/issues/7/labels": ok("[" + labelJSON(101, "falconet") + "," + labelJSON(102, "needs-info") + "]"),
	})
	c := client(t, s)
	err := c.AddIssueLabels("o", "r", 7, []string{"needs-info", "Needs-Info-typo"})
	if err == nil || !strings.Contains(err.Error(), `"Needs-Info-typo"`) {
		t.Fatalf("got %v, want an error naming Needs-Info-typo", err)
	}
}

func TestRemoveIssueLabelDeletesEveryIDOnTheIssue(t *testing.T) {
	// An organization's label and the repository's own, both named
	// needs-info, with ids the repository's label list would not show
	// together.
	s := serve(t, map[string]answer{
		"GET /repos/o/r/issues/7":                ok(issueWithLabels(7, labelJSON(101, "falconet"), labelJSON(9001, "needs-info"), labelJSON(102, "needs-info"))),
		"DELETE /repos/o/r/issues/7/labels/9001": status(204, ""),
		"DELETE /repos/o/r/issues/7/labels/102":  status(204, ""),
	})
	c := client(t, s)
	if err := c.RemoveIssueLabel("o", "r", 7, "needs-info"); err != nil {
		t.Fatal(err)
	}
	var deleted []string
	for _, r := range s.seen() {
		if r.Method == "DELETE" {
			deleted = append(deleted, r.Path)
		}
	}
	if !reflect.DeepEqual(deleted, []string{"/repos/o/r/issues/7/labels/9001", "/repos/o/r/issues/7/labels/102"}) {
		t.Fatalf("deleted %v", deleted)
	}
}

func TestRemoveALabelNotOnTheIssueIs404WithNoDelete(t *testing.T) {
	s := serve(t, map[string]answer{
		"GET /repos/o/r/issues/7": ok(issueWithLabels(7, labelJSON(101, "falconet"), labelJSON(102, "Needs-Info"))),
	})
	c := client(t, s)
	err := c.RemoveIssueLabel("o", "r", 7, "needs-info")
	var e *forge.Error
	if !errors.As(err, &e) || e.Status != http.StatusNotFound {
		t.Fatalf("got %v, want a 404 *forge.Error", err)
	}
	for _, r := range s.seen() {
		if r.Method == "DELETE" {
			t.Errorf("sent %s %s", r.Method, r.Path)
		}
	}
}

// --- assignees ---------------------------------------------------------------

func TestAssigneesAreMergedAndPatched(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []string
		add      bool
		logins   []string
		want     string
	}{
		{"add to one", []string{"a"}, true, []string{"B", "b"}, `{"assignees":["a","B"]}`},
		{"add an existing login in another case", []string{"a"}, true, []string{"A"}, `{"assignees":["a"]}`},
		{"add to none", nil, true, []string{"zetlen"}, `{"assignees":["zetlen"]}`},
		{"remove the last", []string{"a"}, false, []string{"A"}, `{"assignees":[]}`},
		{"remove from none", nil, false, []string{"a"}, `{"assignees":[]}`},
		{"remove one of two", []string{"a", "b"}, false, []string{"b"}, `{"assignees":["a"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serve(t, map[string]answer{
				"GET /repos/o/r/issues/7":   ok(issueJSON(7, tc.existing, false)),
				"PATCH /repos/o/r/issues/7": status(201, issueJSON(7, nil, false)),
			})
			c := client(t, s)
			var err error
			if tc.add {
				err = c.AddIssueAssignees("o", "r", 7, tc.logins)
			} else {
				err = c.RemoveIssueAssignees("o", "r", 7, tc.logins)
			}
			if err != nil {
				t.Fatal(err)
			}
			patch := requests(s, "PATCH", "/repos/o/r/issues/7")
			if len(patch) != 1 || patch[0].Body != tc.want {
				t.Fatalf("patched %+v, want %s", patch, tc.want)
			}
		})
	}

	s := serve(t, map[string]answer{
		"GET /repos/o/r/issues/7":   ok(issueJSON(7, nil, false)),
		"PATCH /repos/o/r/issues/7": status(422, `{"message":"Assignee does not exist: [name: ghost]","url":"https://got.example/api/swagger"}`),
	})
	if err := client(t, s).AddIssueAssignees("o", "r", 7, []string{"ghost"}); err == nil {
		t.Error("a 422 answered nil")
	}
}

// --- what the adapter can reach ----------------------------------------------

// drive calls every method of forge.Client on the repository owner/name,
// each with arguments that reach its routes, and returns the errors it saw.
func drive(t *testing.T, c forge.Client, owner, name string) []error {
	t.Helper()
	var errs []error
	note := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	_, err := c.GetIssue(owner, name, 7)
	note(err)
	_, err = c.GetIssueRaw(owner, name, 7)
	note(err)
	_, err = c.ListIssueComments(owner, name, 7)
	note(err)
	_, err = c.ListIssueCommentsRaw(owner, name, 7)
	note(err)
	_, err = c.ListOpenPulls(owner, name)
	note(err)
	_, err = c.GetAuthenticatedUser()
	note(err)
	_, err = c.RepoPermission(owner, name, "maint")
	note(err)
	note(c.CreateIssueComment(owner, name, 7, "hello"))
	note(c.AddIssueLabels(owner, name, 7, []string{"needs-info"}))
	note(c.RemoveIssueLabel(owner, name, 7, "needs-info"))
	note(c.AddIssueAssignees(owner, name, 7, []string{"zetlen"}))
	note(c.RemoveIssueAssignees(owner, name, 7, []string{"zetlen"}))
	return errs
}

func everyRoute(t *testing.T) *server {
	t.Helper()
	return serve(t, map[string]answer{
		"GET /repos/owner/name/issues/7":                       ok(strings.Replace(issueJSON(7, []string{"zetlen"}, false), labelJSON(1, "falconet"), labelJSON(102, "needs-info"), 1)),
		"PATCH /repos/owner/name/issues/7":                     status(201, issueJSON(7, nil, false)),
		"GET /repos/owner/name/issues/7/comments":              ok(`[]`),
		"POST /repos/owner/name/issues/7/comments":             status(201, `{"id":1,"body":"hello"}`),
		"POST /repos/owner/name/issues/7/labels":               ok("[" + labelJSON(102, "needs-info") + "]"),
		"DELETE /repos/owner/name/issues/7/labels/102":         status(204, ""),
		"GET /repos/owner/name/pulls":                          paged(nil, 50),
		"GET /repos/owner/name/collaborators/maint/permission": ok(`{"permission":"write","role_name":"write"}`),
	})
}

func TestTheAdapterReachesOnlyTheseRoutes(t *testing.T) {
	s := everyRoute(t)
	if errs := drive(t, client(t, s), "owner", "name"); len(errs) != 0 {
		t.Fatalf("driving every method: %v", errs)
	}
	template := strings.NewReplacer(
		"/repos/owner/name", "/repos/{owner}/{name}",
		"/issues/7", "/issues/{n}",
		"/labels/102", "/labels/{id}",
		"/collaborators/maint/", "/collaborators/{login}/",
	)
	seen := map[string]bool{}
	for _, r := range s.seen() {
		seen[r.Method+" "+template.Replace(r.Path)] = true
	}
	var got []string
	for k := range seen {
		got = append(got, k)
	}
	// Every allowed route is one a method reaches: a route nothing sends is
	// reach the token does not need.
	want := append([]string(nil), allowed...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routes reached:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	never := regexp.MustCompile(`/merge|branch_protections|/hooks|/keys|/actions/secrets|/actions/variables|/admin|/collaborators/[^/]+$`)
	for _, r := range s.seen() {
		if never.MatchString(r.Path) {
			t.Errorf("reached %s %s", r.Method, r.Path)
		}
	}
}

func TestTheTokenTravelsInTheHeaderOnly(t *testing.T) {
	s := everyRoute(t)
	errs := drive(t, client(t, s), "owner", "name")

	// Every refusal too: the same calls against a server that refuses them all.
	refusing := serve(t, map[string]answer{
		"GET /user": status(401, `{"message":"user does not exist","url":"https://got.example/api/swagger"}`),
	})
	errs = append(errs, drive(t, client(t, refusing), "owner", "name")...)
	if len(errs) == 0 {
		t.Fatal("the refusing server refused nothing")
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), token) {
			t.Errorf("an error carries the token: %v", err)
		}
	}
	for _, r := range append(s.seen(), refusing.seen()...) {
		if r.Auth != "token "+token {
			t.Errorf("%s %s: Authorization %q", r.Method, r.Path, r.Auth)
		}
		if strings.Contains(r.Path, token) || strings.Contains(r.Query, token) || strings.Contains(r.Body, token) {
			t.Errorf("%s %s?%s carries the token outside its header", r.Method, r.Path, r.Query)
		}
	}
}
