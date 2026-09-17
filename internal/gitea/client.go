// Package gitea is falconet's adapter to the Gitea API: Client, the
// forge.Client that speaks Gitea's REST API (/api/v1) through net/http.
//
// The token it holds belongs to a bot user who is an Administrator of the
// repository, because Gitea answers a login's permission on a repository only
// to a site administrator, a repository administrator, or the login itself
// (gitea routers/api/v1/repo/collaborators.go:279). That token can merge,
// change branch protection, collaborators and secrets, and delete the
// repository. falconet merges nothing (principle 5), so a Client sends
// exactly the requests in allowed and refuses any other before it is built.
//
// Nothing retries. A call that fails is a *forge.Error carrying the status
// and the message Gitea sent, and a 404 reads "not found, or no access",
// because Gitea answers a token that cannot see a repository exactly as it
// answers a name that does not exist.
//
// A comment that cites Gitea's source by file and line cites Gitea v1.26.4.
package gitea

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zetlen/falconet/internal/forge"
)

const (
	// timeout bounds one request, connection to last byte of the answer.
	timeout = 30 * time.Second
	// maxBody is the most of an answer the adapter reads.
	maxBody = 8 << 20
	// maxMessage is the most of an error's message an error carries.
	maxMessage = 512
	// pageSize is what a list asks for. The instance caps it at its own
	// MAX_RESPONSE_ITEMS, which may be lower.
	pageSize = 50
	// maxPages is the most pages of answers a list reads before its empty
	// page.
	maxPages = 20
)

// allowed is every request a Client sends: a method and a path below the API
// base, where {owner}, {name} and {login} are one path segment and {n} and
// {id} are digits.
var allowed = []string{
	"GET /user",
	"GET /repos/{owner}/{name}/issues/{n}",
	"PATCH /repos/{owner}/{name}/issues/{n}",
	"GET /repos/{owner}/{name}/issues/{n}/comments",
	"POST /repos/{owner}/{name}/issues/{n}/comments",
	"POST /repos/{owner}/{name}/issues/{n}/labels",
	"DELETE /repos/{owner}/{name}/issues/{n}/labels/{id}",
	"GET /repos/{owner}/{name}/pulls",
	"GET /repos/{owner}/{name}/collaborators/{login}/permission",
}

var allowedRoutes = compileRoutes(allowed)

func compileRoutes(list []string) map[string][]*regexp.Regexp {
	segment := strings.NewReplacer(
		"{owner}", `[^/]+`, "{name}", `[^/]+`, "{login}", `[^/]+`,
		"{n}", `[0-9]+`, "{id}", `[0-9]+`,
	)
	out := map[string][]*regexp.Regexp{}
	for _, r := range list {
		method, path, _ := strings.Cut(r, " ")
		out[method] = append(out[method], regexp.MustCompile("^"+segment.Replace(path)+"$"))
	}
	return out
}

func isAllowed(method, path string) bool {
	for _, re := range allowedRoutes[method] {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// Client is a forge.Client for one Gitea instance, holding one bot user's
// token.
type Client struct {
	base  string
	token string
	bot   string
	hc    *http.Client

	once    sync.Once
	login   string
	selfErr error
}

var _ forge.Client = (*Client)(nil)

// New is a Client that sends token to the Gitea API at apiURL (the instance
// URL followed by /api/v1) on behalf of the user bot.
func New(apiURL, token, bot string) (*Client, error) {
	// The token acts as a repository administrator, so a Client that does
	// not know whose it is must not exist: an empty token or bot login is
	// refused before anything is built.
	if token == "" {
		return nil, errors.New("gitea: no token")
	}
	if !isLogin(bot) {
		return nil, fmt.Errorf("gitea: %q is not a login", bot)
	}
	base, err := apiBase(apiURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		base:  base,
		token: token,
		bot:   bot,
		hc: &http.Client{
			Timeout: timeout,
			// A token that can administer the repository never follows a
			// redirect: a 3xx names a host or a path the caller did not,
			// so it is answered as the refusal it is, and the token goes
			// nowhere else.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// apiBase is apiURL without its trailing slash, when it is one the token may
// travel to.
func apiBase(apiURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil {
		return "", fmt.Errorf("gitea: %q is not a URL: %v", apiURL, err)
	}
	// Every route is appended to the base as a path, so a base with a query,
	// even an empty one after a bare "?", turns each route into query text.
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("gitea: %q is not an API base URL", apiURL)
	}
	// The token must never cross a network in plaintext: the API is reached
	// over https, and http only on this machine's loopback, where a test's
	// server listens.
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return "", fmt.Errorf("gitea: %q is not https; the token travels only over https or to loopback", apiURL)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// isLoopback is a literal loopback address. A name is never loopback: even
// "localhost" resolves through the hosts file and DNS, which can answer
// with a remote address.
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLogin is a login, owner or repository name that may go in a path: the
// forge's alphabet, and not a dot segment that would name a different path.
func isLogin(s string) bool {
	return forge.IsLoginWord(s) && s != "." && s != ".."
}

// repo refuses an owner or name that would put another path in a request.
func repo(owner, name string) error {
	if !isLogin(owner) || !isLogin(name) {
		return fmt.Errorf("gitea: %q is not an owner/name repository", owner+"/"+name)
	}
	return nil
}

// --- transport ---------------------------------------------------------------

// do makes one request, after the self-check. in, when not nil, is sent as
// JSON; out, when not nil, is filled from the JSON answer. Any status outside
// 2xx is a *forge.Error.
func (c *Client) do(method, path string, query url.Values, in, out any) error {
	if err := c.self(); err != nil {
		return err
	}
	return c.send(method, path, query, in, out)
}

// self is the self-check, made once per Client before its first request.
//
// Gitea's users carry no type, so falconet's own comments and labels are told
// from a person's by the bot's login alone. That login must be the token's,
// or falconet's own needs-info question, posted by an account that holds
// admin, would pass the sender rule and start the pipeline again on its own
// question. The result is kept for the Client's lifetime: after a token whose
// user is not the bot, or a failed read of /user, the Client sends nothing
// further.
func (c *Client) self() error {
	c.once.Do(func() {
		var u forge.User
		if err := c.send("GET", "/user", nil, nil, &u); err != nil {
			c.selfErr = err
			return
		}
		if !strings.EqualFold(u.Login, c.bot) {
			c.selfErr = fmt.Errorf("gitea: the token belongs to %q, not the bot %q", u.Login, c.bot)
			return
		}
		c.login = u.Login
	})
	return c.selfErr
}

func (c *Client) send(method, path string, query url.Values, in, out any) error {
	// The token administers the repository, and falconet merges nothing
	// (principle 5): a request that is none of the allowed routes is refused
	// before it is built, whichever method or error branch asks for it.
	if !isAllowed(method, path) {
		return fmt.Errorf("gitea: %s %s is not a route this adapter sends", method, path)
	}
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%s %s: encoding the request: %v", method, path, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return fmt.Errorf("%s %s: %v", method, path, err)
	}
	// The token travels in this header and nowhere else: never in a URL,
	// where a log or a proxy keeps it, and never in an error's text.
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "falconet")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// A *url.Error names the URL, which holds no credential.
		return fmt.Errorf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// An answer is read to maxBody and no further: a larger one is an
	// error, never a truncated document decoded as if it were whole.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("%s %s: reading the answer: %v", method, path, err)
	}
	if len(raw) > maxBody {
		return fmt.Errorf("%s %s: the answer is larger than %d bytes", method, path, maxBody)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &forge.Error{Method: method, Path: path, Status: resp.StatusCode, Message: c.message(raw)}
	}
	if out != nil {
		// Every route read with out answers a document. An empty body or
		// null would leave out at its zero value, which reads as an issue
		// with no labels or a repository with no open pull request, so it is
		// an error.
		if doc := bytes.TrimSpace(raw); len(doc) == 0 || bytes.Equal(doc, []byte("null")) {
			return fmt.Errorf("%s %s: answered %d with no document", method, path, resp.StatusCode)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: decoding the answer: %v", method, path, err)
		}
	}
	return nil
}

// message is the error message in raw, as an error may carry it: a server or
// a proxy in front of it can echo the request's Authorization header into
// its message, and an error's text reaches Actions logs, so the token is
// cut out of it, and the message is cut to maxMessage bytes.
func (c *Client) message(raw []byte) string {
	msg := strings.ReplaceAll(forge.Message(raw), c.token, "[token]")
	if len(msg) > maxMessage {
		msg = strings.ToValidUTF8(msg[:maxMessage], "") + "..."
	}
	return msg
}

// pages reads a list route page by page into each, until a page comes back
// empty.
//
// Gitea caps `limit` at the instance's MAX_RESPONSE_ITEMS (gitea
// services/convert/utils.go:18), so a page shorter than the limit asked for
// is not the last page, and a list that stopped there would miss what the
// next page holds. Only an empty page ends a list. A list longer than
// maxPages is an error, never a partial answer.
func pages[T any](c *Client, path string, query url.Values, each func([]T)) error {
	for page := 1; page <= maxPages+1; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("page", strconv.Itoa(page))
		var batch []T
		if err := c.do("GET", path, q, nil, &batch); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		each(batch)
	}
	return fmt.Errorf("GET %s: more than %d pages", path, maxPages)
}

// --- reads -------------------------------------------------------------------

// issue is GET /repos/{owner}/{name}/issues/{n} as Gitea sent it.
//
// Gitea answers 200 with an empty issue when it fails to load part of one
// (gitea services/convert/issue.go:37). Read as an issue, that answer has no
// labels and no assignees, and the assignee PATCH built from it unassigns
// everyone. An answer whose number is not the number asked for is an error.
func (c *Client) issue(owner, name string, number int) (json.RawMessage, error) {
	if err := repo(owner, name); err != nil {
		return nil, err
	}
	path := forge.IssuePath(owner, name, number, "")
	var raw json.RawMessage
	if err := c.do("GET", path, nil, nil, &raw); err != nil {
		return nil, err
	}
	var head struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("GET %s: decoding the answer: %v", path, err)
	}
	if head.Number != number {
		return nil, fmt.Errorf("GET %s: answered issue number %d, not %d", path, head.Number, number)
	}
	return raw, nil
}

func (c *Client) GetIssue(owner, name string, number int) (*forge.Issue, error) {
	raw, err := c.issue(owner, name, number)
	if err != nil {
		return nil, err
	}
	var out forge.Issue
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("GET %s: decoding the answer: %v", forge.IssuePath(owner, name, number, ""), err)
	}
	return &out, nil
}

func (c *Client) GetIssueRaw(owner, name string, number int) (json.RawMessage, error) {
	return c.issue(owner, name, number)
}

// ListIssueComments is GET …/issues/{n}/comments, the whole thread in one
// answer: Gitea's route takes no paging parameters and returns every comment.
func (c *Client) ListIssueComments(owner, name string, number int) ([]forge.IssueComment, error) {
	if err := repo(owner, name); err != nil {
		return nil, err
	}
	var out []forge.IssueComment
	if err := c.do("GET", forge.IssuePath(owner, name, number, "comments"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListIssueCommentsRaw is ListIssueComments' answer as Gitea sent it.
func (c *Client) ListIssueCommentsRaw(owner, name string, number int) (json.RawMessage, error) {
	if err := repo(owner, name); err != nil {
		return nil, err
	}
	var out json.RawMessage
	if err := c.do("GET", forge.IssuePath(owner, name, number, "comments"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListOpenPulls is every open pull request, read with `limit` and `page` to
// an empty page. Gitea pages by offset, so a pull request that closes while
// the pages are read moves every later one up a place, and one open pull
// request can be missed without an error.
func (c *Client) ListOpenPulls(owner, name string) ([]forge.PullRequest, error) {
	if err := repo(owner, name); err != nil {
		return nil, err
	}
	var out []forge.PullRequest
	err := pages(c, forge.RepoPath(owner, name, "/pulls"), url.Values{"state": {"open"}},
		func(batch []forge.PullRequest) { out = append(out, batch...) })
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetAuthenticatedUser is the token's user, as the self-check read it.
func (c *Client) GetAuthenticatedUser() (*forge.User, error) {
	if err := c.self(); err != nil {
		return nil, err
	}
	return &forge.User{Login: c.login}, nil
}

// RepoPermission is GET /repos/{owner}/{name}/collaborators/{login}/permission.
//
// Gitea's `permission` is the login's access to the repository as a whole
// (gitea routers/api/v1/repo/collaborators.go:300). It counts the owner, a
// site administrator, a direct collaborator's grant, and membership of an
// organization team with administrator access. It does not count a team
// without administrator access: Gitea keeps such a team's read or write in
// its per-unit grants (gitea models/perm/access/repo_permission.go:490),
// which this route does not read. A login whose write comes only from such a
// team answers read on a public repository and none on a private one.
//
// Gitea's words are none, read, write, admin and owner (gitea
// models/perm/access_mode.go:26). Owner is admin. Any other word, or a
// missing permission field, is an error.
//
// Gitea answers this route only to a site administrator, a repository
// administrator, or the login asking about itself, and anyone else gets 403.
// That 403 is an error that names the requirement, because it is the bot's
// standing, not the sender's, that is wrong.
func (c *Client) RepoPermission(owner, name, login string) (forge.Permission, error) {
	if err := repo(owner, name); err != nil {
		return "", err
	}
	if !isLogin(login) {
		return "", fmt.Errorf("%q is not a login", login)
	}
	path := forge.RepoPath(owner, name, "/collaborators/"+url.PathEscape(login)+"/permission")
	var out struct {
		Permission *string `json:"permission"`
	}
	if err := c.do("GET", path, nil, nil, &out); err != nil {
		var apiErr *forge.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
			return "", fmt.Errorf("%w (the token's user must be an administrator of %s/%s to read another account's permission)", err, owner, name)
		}
		return "", err
	}
	word := ""
	if out.Permission != nil {
		word = *out.Permission
		switch word {
		case "owner":
			return forge.PermissionAdmin, nil
		case "admin", "write", "read", "none":
			return forge.Permission(word), nil
		}
	}
	return "", fmt.Errorf("GET %s: answered permission %q, which is none of owner, admin, write, read, none", path, word)
}

// --- labels ------------------------------------------------------------------

// label is a label as Gitea puts it on an issue: a repository's own label or
// its organization's, with the id the issue routes take.
type label struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// AddIssueLabels puts the named labels on the issue.
//
// Gitea looks each name up among the repository's labels and, when an
// organization owns the repository, the organization's labels. It drops a
// name it finds in neither and still answers 200 (gitea
// routers/api/v1/repo/issue_label.go:348-361). pause's word `failure` rests
// on a label that did not land being an error, so the answer, the issue's
// labels after the change, must hold every name posted.
func (c *Client) AddIssueLabels(owner, name string, number int, labels []string) error {
	if err := repo(owner, name); err != nil {
		return err
	}
	path := forge.IssuePath(owner, name, number, "labels")
	var after []label
	if err := c.do("POST", path, nil, map[string][]string{"labels": labels}, &after); err != nil {
		return err
	}
	on := map[string]bool{}
	for _, l := range after {
		on[l.Name] = true
	}
	for _, n := range labels {
		if !on[n] {
			return fmt.Errorf("POST %s: answered without label %q; Gitea adds only a label the repository or its organization defines", path, n)
		}
	}
	return nil
}

// RemoveIssueLabel takes every label named lbl off the issue.
//
// Gitea removes a label by id, and the issue's own labels are where the id
// is read: they hold the organization's labels as well as the repository's,
// and a repository and its organization can each have a label of that name.
// A name that is on the issue under no id is a 404 *forge.Error and nothing
// is deleted, as GitHub answers for a label that is not on the issue.
func (c *Client) RemoveIssueLabel(owner, name string, number int, lbl string) error {
	raw, err := c.issue(owner, name, number)
	if err != nil {
		return err
	}
	var issue struct {
		Labels []label `json:"labels"`
	}
	if err := json.Unmarshal(raw, &issue); err != nil {
		return fmt.Errorf("GET %s: decoding the answer: %v", forge.IssuePath(owner, name, number, ""), err)
	}
	var ids []int64
	for _, l := range issue.Labels {
		if l.Name == lbl {
			ids = append(ids, l.ID)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("#%d has no label %q: %w", number, lbl,
			&forge.Error{Method: "GET", Path: forge.IssuePath(owner, name, number, ""), Status: http.StatusNotFound})
	}
	// A label that leaves the issue between the read and the delete answers
	// 204 (gitea models/issues/issue_label.go:164), which is success.
	for _, id := range ids {
		if err := c.do("DELETE", forge.IssuePath(owner, name, number, "labels/"+strconv.FormatInt(id, 10)), nil, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// --- writes ------------------------------------------------------------------

func (c *Client) CreateIssueComment(owner, name string, number int, body string) error {
	if err := repo(owner, name); err != nil {
		return err
	}
	return c.do("POST", forge.IssuePath(owner, name, number, "comments"),
		nil, map[string]string{"body": body}, nil)
}

// AddIssueAssignees adds logins to the issue's assignees. Gitea has no route
// that adds or removes one assignee, so this reads the issue and PATCHes the
// whole set; a change another client makes between the two is lost.
func (c *Client) AddIssueAssignees(owner, name string, number int, logins []string) error {
	return c.editAssignees(owner, name, number, func(set []string) []string {
		for _, l := range logins {
			if !containsFold(set, l) {
				set = append(set, l)
			}
		}
		return set
	})
}

// RemoveIssueAssignees takes logins off the issue's assignees, by the same
// read and PATCH as AddIssueAssignees.
func (c *Client) RemoveIssueAssignees(owner, name string, number int, logins []string) error {
	return c.editAssignees(owner, name, number, func(set []string) []string {
		var kept []string
		for _, l := range set {
			if !containsFold(logins, l) {
				kept = append(kept, l)
			}
		}
		return kept
	})
}

func (c *Client) editAssignees(owner, name string, number int, change func([]string) []string) error {
	issue, err := c.GetIssue(owner, name, number)
	if err != nil {
		return err
	}
	var set []string
	for _, u := range issue.Assignees {
		set = append(set, u.Login)
	}
	set = change(set)
	// The set is sent as a JSON array, [] when it is empty: Gitea reads a
	// null `assignees` as "leave them as they are" (gitea
	// routers/api/v1/repo/issue.go:863), so an unassignment sent as null
	// would answer 201 and change nothing.
	if set == nil {
		set = []string{}
	}
	return c.do("PATCH", forge.IssuePath(owner, name, number, ""),
		nil, map[string][]string{"assignees": set}, nil)
}

func containsFold(list []string, s string) bool {
	for _, l := range list {
		if strings.EqualFold(l, s) {
			return true
		}
	}
	return false
}
