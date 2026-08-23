// Package appmanifest is README step 3 — the GitHub App — done by manifest,
// as pure functions: the App's name, the manifest GitHub is handed, the page
// the browser is sent to, the check on what GitHub sends back, and the two
// URLs a person is asked to open. The verb, cmd/falconet/init_app.go, is the
// listener, the browser, the API calls and the polling; what it hands in
// here is what it knows, and what it gets back is bytes and strings.
//
// Nothing here touches the filesystem, the network or the environment, and
// the one thing that needs randomness — the nonce — takes its bytes from the
// caller, so every decision can be held to a table and the shapes to
// properties.
//
// # Why by manifest
//
// README step 3 was ten browser steps and a .pem in ~/Downloads: register an
// App by hand, note its ID, generate a private key, download it, `gh secret
// set` it from the download, delete the download. ADR-0006 D5 replaces that
// with GitHub's manifest flow: a form POSTs the App's configuration to
// GitHub, the person clicks one button, GitHub sends the browser back to a
// listener on localhost with a temporary code, and POST
// /app-manifests/{code}/conversions answers with the App's ID and its
// private key. The key goes from that response into a sealed box and into
// the repository's secrets; it is never written to disk, and the README's
// `rm ~/Downloads/…pem` step has nothing left to remove.
//
// # A browser cannot POST from a URL
//
// GitHub's manifest flow takes the manifest as a form field named
// `manifest`, POSTed to /settings/apps/new. There is no way to make a
// browser POST by handing it a URL, so the verb listens on loopback and
// serves a page that carries the form and submits itself; the person sees
// GitHub's page, not this one. The same listener is where GitHub sends the
// browser back.
//
// # The state is a nonce, and a mismatch is refused
//
// The form's action carries ?state=<nonce>, and GitHub echoes it on the
// redirect. The listener is loopback-only, but a browser tab is not: any
// page the person has open could send their browser to
// http://127.0.0.1:<port>/callback?code=…. A code that arrives without the
// nonce this run minted is not the code this run asked for, and converting
// it would seal a stranger's App into the repository's secrets. So the
// state must match exactly, and a mismatch is a 400 to the browser and a
// refusal here.
package appmanifest

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode/utf8"
)

// NameLimit is GitHub's limit on an App's name: 34 characters. A longer
// default is cut; a longer --app-name is refused before the browser opens,
// since GitHub's page would refuse it after.
const NameLimit = 34

// Name is the App's name: explicit when --app-name gave one, else
// falconet-<owner>-<repo>, cut to NameLimit when that is longer. cut reports
// whether it was, so the verb can say so; a cut never leaves a trailing
// dash, which GitHub's page would reject. An explicit name over the limit is
// an error naming it.
func Name(explicit, owner, repo string) (name string, cut bool, err error) {
	if explicit != "" {
		if n := utf8.RuneCountInString(explicit); n > NameLimit {
			return "", false, fmt.Errorf("--app-name is %d characters, and GitHub allows %d", n, NameLimit)
		}
		return explicit, false, nil
	}
	name = "falconet-" + owner + "-" + repo
	if len(name) <= NameLimit {
		return name, false, nil
	}
	// Owner and repository names are ASCII (github.SplitRepository admits
	// nothing else), so a byte cut is a character cut.
	return strings.TrimRight(name[:NameLimit], "-"), true, nil
}

// Permissions is what the App is granted, and nothing else: the three
// README step 3 names. Contents to push the branch, Issues for the queue
// and the hand-over comments, Pull requests to open one. The App is a
// credential and not a service, so there are no events and no webhook.
var Permissions = map[string]string{
	"contents":      "write",
	"issues":        "write",
	"pull_requests": "write",
}

// Manifest is the JSON GitHub's manifest flow is handed, indented for the
// person who reads the page before it submits. Exactly these fields:
//
//   - name, the App's;
//   - url, the repository — GitHub requires a homepage and falconet has
//     nothing else to point it at;
//   - hook_attributes, with active false: the App receives no events. The
//     documentation's manifest examples always carry hook_attributes.url and
//     its parameter table marks url as required within the object, so the
//     listener's own URL is given — it answers nothing and is gone when
//     the run ends, and an inactive hook delivers nowhere;
//   - redirect_url, where GitHub sends the browser back with the code;
//   - public false, so the App is installable only by its owner — README
//     step 3's "only on this account";
//   - default_permissions, the three;
//   - default_events, none.
//
// No callback_urls, no setup_url, no description: each would be a surface
// the App does not use.
func Manifest(name, repoURL, listenerURL, redirectURL string) []byte {
	m := manifest{
		Name:           name,
		URL:            repoURL,
		HookAttributes: hookAttributes{URL: listenerURL, Active: false},
		RedirectURL:    redirectURL,
		Public:         false,
		Permissions:    Permissions,
		Events:         []string{},
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		// A struct of strings and bools cannot fail to marshal.
		panic(err)
	}
	return out
}

type manifest struct {
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	HookAttributes hookAttributes    `json:"hook_attributes"`
	RedirectURL    string            `json:"redirect_url"`
	Public         bool              `json:"public"`
	Permissions    map[string]string `json:"default_permissions"`
	Events         []string          `json:"default_events"`
}

type hookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// Nonce is the state parameter, as hex of the random bytes the caller
// read: 32 bytes from crypto/rand is 64 hex characters. Fewer than 32 bytes
// is refused — a short nonce is a guessable one.
func Nonce(random []byte) (string, error) {
	if len(random) < 32 {
		return "", fmt.Errorf("a nonce needs 32 random bytes, not %d", len(random))
	}
	return hex.EncodeToString(random), nil
}

// FormAction is where the form POSTs: GitHub's new-App page for the person,
// or the organisation's when the repository belongs to one — a manifest
// POSTed to the personal page registers the App under the person, where it
// could not be installed on an organisation's repository. state is the
// nonce, which GitHub echoes on the redirect.
func FormAction(serverURL, org, nonce string) string {
	base := strings.TrimRight(serverURL, "/")
	if org != "" {
		return base + "/organizations/" + url.PathEscape(org) + "/settings/apps/new?state=" + url.QueryEscape(nonce)
	}
	return base + "/settings/apps/new?state=" + url.QueryEscape(nonce)
}

// FormPage is the page the listener serves at /: a form whose one field,
// manifest, is the manifest, whose action is FormAction, and which submits
// itself on load — with a visible button for a browser that runs no script,
// and the manifest in view for a person who wants to read what is about to
// be registered before it is. The person is told what to click on GitHub's
// side, since that page is GitHub's and says nothing about falconet.
func FormPage(action string, manifest []byte) []byte {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<title>falconet init: register the GitHub App</title>\n")
	b.WriteString("<style>body{font-family:system-ui,sans-serif;max-width:48em;margin:3em auto;padding:0 1em}textarea{width:100%;font-family:monospace}button{font-size:1.1em;padding:.5em 1em}</style>\n")
	b.WriteString("</head>\n<body>\n")
	b.WriteString("<h1>falconet init</h1>\n")
	b.WriteString("<p>This page is served by <code>falconet init</code> on your machine. It sends the App configuration below to GitHub, ")
	b.WriteString("where you click <strong>Create GitHub App</strong>; GitHub then sends this browser back here, and init stores the App's ID and private key as repository secrets. ")
	b.WriteString("The private key is never written to disk.</p>\n")
	b.WriteString("<form id=\"manifest\" method=\"post\" action=\"" + html.EscapeString(action) + "\">\n")
	b.WriteString("<p><textarea name=\"manifest\" rows=\"20\" readonly>")
	b.Write([]byte(html.EscapeString(string(manifest))))
	b.WriteString("</textarea></p>\n")
	b.WriteString("<p><button type=\"submit\">Continue to GitHub</button> <span>(if nothing happens on its own, click this)</span></p>\n")
	b.WriteString("</form>\n")
	b.WriteString("<script>document.getElementById(\"manifest\").submit();</script>\n")
	b.WriteString("</body>\n</html>\n")
	return []byte(b.String())
}

// CallbackPage is what the browser sees after a redirect the listener
// accepted: the person's work in this tab is done, and the rest happens in
// the terminal.
func CallbackPage(name string) []byte {
	return []byte("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>falconet init: registered</title>" +
		"<style>body{font-family:system-ui,sans-serif;max-width:48em;margin:3em auto;padding:0 1em}</style></head>\n" +
		"<body><h1>Registered</h1><p>GitHub has registered <strong>" + html.EscapeString(name) + "</strong>. " +
		"Back in the terminal, <code>falconet init</code> is storing its secrets and will open the install page next. You can close this tab.</p></body></html>\n")
}

// Callback reads what GitHub sent the browser back with. The state must be
// the nonce this run minted, exactly; the code must be present. Either
// missing or wrong is an error naming which, and the caller answers the
// browser with a 400 and keeps listening.
func Callback(query url.Values, nonce string) (code string, err error) {
	state := query.Get("state")
	switch {
	case state == "":
		return "", fmt.Errorf("no state on the redirect — refusing the code")
	case state != nonce:
		return "", fmt.Errorf("state mismatch — refusing the code")
	}
	code = query.Get("code")
	if code == "" {
		return "", fmt.Errorf("no code on the redirect")
	}
	return code, nil
}

// InstallURL is where a person installs the App: GitHub's page for a new
// installation of it, by slug.
func InstallURL(serverURL, slug string) string {
	return strings.TrimRight(serverURL, "/") + "/apps/" + url.PathEscape(slug) + "/installations/new"
}

// ListenerURL is the listener's own address, as the manifest names it and
// as a person is told to open it.
func ListenerURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
}

// RedirectURL is where GitHub sends the browser back: the listener's
// /callback.
func RedirectURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", port)
}
