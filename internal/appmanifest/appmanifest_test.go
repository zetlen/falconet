package appmanifest

import (
	"encoding/hex"
	"encoding/json"
	"html"
	"math/rand"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/quick"
)

const maxCount = 5000

func check(t *testing.T, f any) {
	t.Helper()
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// --- the name --------------------------------------------------------------

func TestNameIsExplicitOrTheDefault(t *testing.T) {
	for _, tc := range []struct {
		explicit, owner, repo string
		want                  string
		cut                   bool
	}{
		{"", "zetlen", "wayfinders-infra", "falconet-zetlen-wayfinders-infra", false},
		{"My Falconet", "zetlen", "wayfinders-infra", "My Falconet", false},
		{"", "zetlen", "a-very-long-repository-name-indeed-yes", "falconet-zetlen-a-very-long-reposi", true},
		{"", "o", "r", "falconet-o-r", false},
		{"", strings.Repeat("x", 25), "y", "falconet-" + strings.Repeat("x", 25), true},
		// Exactly the limit is not cut.
		{"", "abcdefghijklmnopqrstuvw", "r", "falconet-abcdefghijklmnopqrstuvw-r", false},
		{strings.Repeat("é", NameLimit), "o", "r", strings.Repeat("é", NameLimit), false},
	} {
		got, cut, err := Name(tc.explicit, tc.owner, tc.repo)
		if err != nil {
			t.Errorf("Name(%q, %q, %q): %v", tc.explicit, tc.owner, tc.repo, err)
			continue
		}
		if got != tc.want || cut != tc.cut {
			t.Errorf("Name(%q, %q, %q) = %q, %v; want %q, %v", tc.explicit, tc.owner, tc.repo, got, cut, tc.want, tc.cut)
		}
		if len([]rune(got)) > NameLimit {
			t.Errorf("Name(%q, %q, %q) = %q is over the limit", tc.explicit, tc.owner, tc.repo, got)
		}
	}
}

func TestACutNeverLeavesATrailingDash(t *testing.T) {
	// "falconet-" is 9, the owner 24: the 34th character is the dash before
	// the repository.
	got, cut, err := Name("", strings.Repeat("a", 24), "repo")
	if err != nil || !cut {
		t.Fatalf("got %q, %v, %v", got, cut, err)
	}
	if strings.HasSuffix(got, "-") || got != "falconet-"+strings.Repeat("a", 24) {
		t.Errorf("cut to %q", got)
	}
}

func TestAnExplicitNameOverTheLimitIsRefused(t *testing.T) {
	_, _, err := Name(strings.Repeat("x", NameLimit+1), "o", "r")
	if err == nil || !strings.Contains(err.Error(), "35") || !strings.Contains(err.Error(), "34") {
		t.Errorf("got %v", err)
	}
	if _, _, err := Name(strings.Repeat("x", NameLimit), "o", "r"); err != nil {
		t.Errorf("exactly the limit: %v", err)
	}
	// Characters, not bytes: 34 two-byte characters are 34 characters.
	if _, _, err := Name(strings.Repeat("é", NameLimit), "o", "r"); err != nil {
		t.Errorf("34 multibyte characters: %v", err)
	}
}

// word is an owner or repository name: GitHub's alphabet, 1–40 characters.
type word string

func (word) Generate(r *rand.Rand, _ int) reflect.Value {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
	n := 1 + r.Intn(40)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return reflect.ValueOf(word(b))
}

// For any owner and repository, the default name is at most the limit,
// starts with falconet-, is cut exactly when the full name is over the
// limit, is the full name when it is not, and never ends in a dash when cut.
func TestTheDefaultNameAlwaysFits(t *testing.T) {
	check(t, func(owner, repo word) bool {
		name, cut, err := Name("", string(owner), string(repo))
		if err != nil {
			return false
		}
		full := "falconet-" + string(owner) + "-" + string(repo)
		if len(name) > NameLimit || !strings.HasPrefix(name, "falconet-") {
			return false
		}
		if cut != (len(full) > NameLimit) {
			return false
		}
		if !cut {
			return name == full
		}
		return !strings.HasSuffix(name, "-") && strings.HasPrefix(full, name)
	})
}

// --- the manifest -----------------------------------------------------------

func TestManifestHasExactlyTheseFields(t *testing.T) {
	raw := Manifest("falconet-o-r", "https://github.com/o/r", "http://127.0.0.1:4242/", "http://127.0.0.1:4242/callback")
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	want := map[string]any{
		"name":            "falconet-o-r",
		"url":             "https://github.com/o/r",
		"hook_attributes": map[string]any{"url": "http://127.0.0.1:4242/", "active": false},
		"redirect_url":    "http://127.0.0.1:4242/callback",
		"public":          false,
		"default_permissions": map[string]any{
			"contents":      "write",
			"issues":        "write",
			"pull_requests": "write",
		},
		"default_events": []any{},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest:\n%s\nwant %v", raw, want)
	}
	if len(m) != 7 {
		t.Errorf("%d fields, want 7: %v", len(m), m)
	}
}

func TestManifestIsIndentedForAPerson(t *testing.T) {
	raw := Manifest("n", "u", "l", "r")
	if !json.Valid(raw) || !strings.Contains(string(raw), "\n  \"") {
		t.Errorf("manifest is not indented JSON:\n%s", raw)
	}
}

// For any strings, the manifest carries them where they were put and nothing
// is escaped into something else.
func TestManifestCarriesItsInputs(t *testing.T) {
	check(t, func(name, repoURL, listener, redirect string) bool {
		var m struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Hook struct {
				URL    string `json:"url"`
				Active bool   `json:"active"`
			} `json:"hook_attributes"`
			Redirect string `json:"redirect_url"`
			Public   bool   `json:"public"`
		}
		if err := json.Unmarshal(Manifest(name, repoURL, listener, redirect), &m); err != nil {
			return false
		}
		return m.Name == name && m.URL == repoURL && m.Hook.URL == listener && !m.Hook.Active &&
			m.Redirect == redirect && !m.Public
	})
}

// --- the nonce ---------------------------------------------------------------

func TestNonceIsHexOfThirtyTwoBytes(t *testing.T) {
	random := make([]byte, 32)
	for i := range random {
		random[i] = byte(i)
	}
	nonce, err := Nonce(random)
	if err != nil || len(nonce) != 64 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(nonce) {
		t.Errorf("got %q, %v", nonce, err)
	}
	if _, err := Nonce(random[:31]); err == nil {
		t.Error("31 bytes accepted")
	}
	if _, err := Nonce(nil); err == nil {
		t.Error("no bytes accepted")
	}
}

// For any 32+ bytes the nonce is hex of exactly those bytes: nothing is
// lost, and two different byte strings are two different nonces.
func TestNonceIsLossless(t *testing.T) {
	check(t, func(random [40]byte) bool {
		nonce, err := Nonce(random[:])
		if err != nil {
			return false
		}
		back, err := hex.DecodeString(nonce)
		return err == nil && string(back) == string(random[:])
	})
}

// --- the form ----------------------------------------------------------------

func TestFormActionIsGitHubsNewAppPageOrTheOrganisations(t *testing.T) {
	for _, tc := range []struct {
		server, org, nonce, want string
	}{
		{"https://github.com", "", "abc", "https://github.com/settings/apps/new?state=abc"},
		{"https://github.com/", "", "abc", "https://github.com/settings/apps/new?state=abc"},
		{"https://github.com", "acme", "abc", "https://github.com/organizations/acme/settings/apps/new?state=abc"},
		{"https://ghe.example", "acme", "abc", "https://ghe.example/organizations/acme/settings/apps/new?state=abc"},
		{"https://github.com", "a b", "x&y", "https://github.com/organizations/a%20b/settings/apps/new?state=x%26y"},
	} {
		if got := FormAction(tc.server, tc.org, tc.nonce); got != tc.want {
			t.Errorf("FormAction(%q, %q, %q) = %q, want %q", tc.server, tc.org, tc.nonce, got, tc.want)
		}
	}
}

// textarea is the manifest field's content as the page carries it.
var textarea = regexp.MustCompile(`(?s)<textarea name="manifest"[^>]*>(.*?)</textarea>`)

func TestFormPageCarriesTheNonceAndTheManifest(t *testing.T) {
	manifest := Manifest("falconet-o-r", "https://github.com/o/r", "http://127.0.0.1:1/", "http://127.0.0.1:1/callback")
	action := FormAction("https://github.com", "", "deadbeef")
	page := string(FormPage(action, manifest))
	for _, want := range []string{
		`<form id="manifest" method="post" action="https://github.com/settings/apps/new?state=deadbeef">`,
		`<textarea name="manifest"`,
		`<button type="submit">`,
		`.submit();`,
		`Create GitHub App`,
		`<!doctype html>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	m := textarea.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no manifest textarea:\n%s", page)
	}
	if got := html.UnescapeString(m[1]); got != string(manifest) {
		t.Errorf("the field's content unescapes to\n%s\nwant\n%s", got, manifest)
	}
	if strings.Count(page, `name="manifest"`) != 1 {
		t.Errorf("the form has %d manifest fields, want one", strings.Count(page, `name="manifest"`))
	}
}

// For any manifest bytes and any action, the page's one field unescapes
// back to exactly those bytes — a quote, an ampersand or an angle bracket in
// a name cannot break out of the field — and the action is in the form.
func TestTheFieldRoundTripsAnyManifest(t *testing.T) {
	check(t, func(manifest, nonce string) bool {
		action := FormAction("https://github.com", "", nonce)
		page := string(FormPage(action, []byte(manifest)))
		m := textarea.FindStringSubmatch(page)
		if m == nil || html.UnescapeString(m[1]) != manifest {
			return false
		}
		return strings.Contains(page, `action="`+html.EscapeString(action)+`"`)
	})
}

// --- the callback ------------------------------------------------------------

func TestCallbackDemandsTheNonceAndACode(t *testing.T) {
	const nonce = "0123456789abcdef"
	for _, tc := range []struct {
		query, code, errContains string
	}{
		{"code=abc&state=" + nonce, "abc", ""},
		{"state=" + nonce + "&code=abc", "abc", ""},
		{"code=abc&state=wrong", "", "state mismatch"},
		{"code=abc&state=", "", "no state"},
		{"code=abc", "", "no state"},
		{"state=" + nonce, "", "no code"},
		{"state=" + nonce + "&code=", "", "no code"},
		{"", "", "no state"},
		// A prefix, a suffix, a case change: none is the nonce.
		{"code=abc&state=" + nonce + "0", "", "state mismatch"},
		{"code=abc&state=" + strings.ToUpper(nonce), "", "state mismatch"},
	} {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		code, err := Callback(q, nonce)
		switch {
		case tc.errContains == "" && (err != nil || code != tc.code):
			t.Errorf("Callback(%q): got %q, %v; want %q", tc.query, code, err, tc.code)
		case tc.errContains != "" && (err == nil || !strings.Contains(err.Error(), tc.errContains) || code != ""):
			t.Errorf("Callback(%q): got %q, %v; want an error containing %q", tc.query, code, err, tc.errContains)
		}
	}
}

// For any state that is not the nonce, the code is refused whatever it is;
// for the nonce itself, any non-empty code is accepted as it came.
func TestOnlyTheNonceAdmitsACode(t *testing.T) {
	check(t, func(nonce, state, code string) bool {
		q := url.Values{"state": {state}, "code": {code}}
		got, err := Callback(q, nonce)
		if state != nonce || state == "" {
			return err != nil && got == ""
		}
		if code == "" {
			return err != nil
		}
		return err == nil && got == code
	})
}

// --- the URLs ----------------------------------------------------------------

func TestTheURLs(t *testing.T) {
	if got := InstallURL("https://github.com", "falconet-o-r"); got != "https://github.com/apps/falconet-o-r/installations/new" {
		t.Errorf("InstallURL: %q", got)
	}
	if got := InstallURL("https://github.com/", "a b"); got != "https://github.com/apps/a%20b/installations/new" {
		t.Errorf("InstallURL escaped: %q", got)
	}
	if got := ListenerURL(4242); got != "http://127.0.0.1:4242/" {
		t.Errorf("ListenerURL: %q", got)
	}
	if got := RedirectURL(4242); got != "http://127.0.0.1:4242/callback" {
		t.Errorf("RedirectURL: %q", got)
	}
}
