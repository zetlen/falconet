package main

// init's step 3, by manifest (ADR-0006 D5, #12): the listener on loopback,
// the browser, the conversion, the two sealed secrets, the install page and
// the poll. What is decided — the name, the manifest, the page, the check on
// the redirect, the URLs — is internal/appmanifest; what talks to GitHub is
// internal/github; this file is the sequence, the subprocess that opens a
// browser, and the lines a person reads while it runs.
//
// The private key exists in this process as the bytes of one HTTP response,
// goes into two sealed boxes, and is gone when the run ends. It is never
// written to disk — no temp file — and never printed: not to a log, not in
// an error, not in the report. Everything below that touches it is named
// so a reader can check that list is the whole of it: the response, the
// sealed box, and the JWT that signs the installation poll.

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zetlen/falconet/internal/appmanifest"
	"github.com/zetlen/falconet/internal/doctor"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/setup"
)

// appTimeoutDefault is how long init waits for a person and a browser,
// twice: once for GitHub's redirect after "Create GitHub App", once for the
// installation after "Install". Ten minutes is long enough to read the
// page and short enough that a run nobody is watching ends.
const appTimeoutDefault = 10 * time.Minute

// installPoll is how often GET /repos/{owner}/{repo}/installation is asked
// while a person is clicking through the install page.
const installPoll = 3 * time.Second

// appStep is what step 3 needs from the run: the client and the token, the
// repository as GitHub described it, the flags, the report, and the seam
// #11 left — sealApp stores the two secrets from an ID and a PEM, whichever
// way they were obtained.
type appStep struct {
	client      *github.Client
	token       string
	repo        *github.Repository
	owner, name string
	flags       initFlags
	say         func(doctor.Line)
	left        *leftovers
	sealApp     func(appID string, pem []byte) int
}

// registerApp is README step 3, done by manifest. It returns the exit code
// that stops the run (non-zero: a refusal from GitHub, or the listener
// failing) — or 0, which is also what a step that did not complete returns:
// a redirect that never came, or an installation that was not made in
// time, is reported, left for a person, and the run goes on to the local
// steps. Nothing irreversible has happened at those points that a second
// run cannot carry on from.
func registerApp(s appStep) int {
	timeout := s.flags.timeout
	wait := waitWord(timeout)
	serverURL := "https://" + github.ServerHostFromEnv()
	repository := s.owner + "/" + s.name

	name, cut, err := appmanifest.Name(s.flags.appName, s.owner, s.name)
	if err != nil {
		// Refused at flag time; here only because the signature admits it.
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		return 2
	}
	if cut {
		fmt.Fprintf(os.Stderr, "init: the App name falconet-%s-%s is longer than GitHub's %d characters, so it is %s (--app-name chooses another)\n",
			s.owner, s.name, appmanifest.NameLimit, name)
	}

	// The state: 32 bytes from crypto/rand, which GitHub echoes on the
	// redirect and the listener checks before it accepts a code.
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		fmt.Fprintf(os.Stderr, "init: cannot read random bytes for the state: %v\n", err)
		return 1
	}
	nonce, err := appmanifest.Nonce(random)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		return 1
	}

	// The listener: loopback only, on whatever port is free. It serves the
	// form at / and receives GitHub's redirect at /callback, and nothing
	// else; it is closed the moment a code has arrived.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: cannot listen on 127.0.0.1 for GitHub's redirect: %v\n", err)
		return 1
	}
	port := ln.Addr().(*net.TCPAddr).Port
	listenerURL := appmanifest.ListenerURL(port)
	redirectURL := appmanifest.RedirectURL(port)

	// The manifest names the repository as its homepage: html_url as GitHub
	// sent it, or the URL built from the name when the answer lacked one.
	repoURL := s.repo.HTMLURL
	if repoURL == "" {
		repoURL = serverURL + "/" + repository
	}
	manifest := appmanifest.Manifest(name, repoURL, listenerURL, redirectURL)
	org := ""
	if s.repo.Owner.Type == "Organization" {
		org = s.owner
	}
	page := appmanifest.FormPage(appmanifest.FormAction(serverURL, org, nonce), manifest)

	codes := make(chan string, 1)
	failed := make(chan string, 1)
	var mu sync.Mutex
	mismatches := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code, err := appmanifest.Callback(r.URL.Query(), nonce)
		if err != nil {
			// Refused: the browser is told, the terminal is told, and the
			// listener keeps waiting for the redirect this run asked for.
			// Twice is not a stale tab, and ends the step.
			http.Error(w, "falconet init: "+err.Error(), http.StatusBadRequest)
			fmt.Fprintf(os.Stderr, "init: %v\n", err)
			mu.Lock()
			mismatches++
			n := mismatches
			mu.Unlock()
			if n >= 2 {
				select {
				case failed <- "two redirects arrived with the wrong state":
				default:
				}
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(appmanifest.CallbackPage(name))
		select {
		case codes <- code:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	fmt.Fprintf(os.Stderr, "init: step 3 — registering the GitHub App %s by manifest\n", name)
	openBrowser(listenerURL, s.flags.noBrowser)
	fmt.Fprintf(os.Stderr, "the page takes you to GitHub; click \"Create GitHub App\" there, and GitHub sends the browser back here (waiting up to %s)\n", wait)

	var code string
	select {
	case code = <-codes:
	case why := <-failed:
		return unregistered(s, why)
	case <-time.After(timeout):
		return unregistered(s, "no redirect from GitHub within "+wait)
	}
	_ = srv.Close()

	// The conversion: the code for the App. First with no token, which is
	// what the documentation's bare curl implies for a bootstrap flow; on a
	// 401 or 403, once more with the setup token. Which one worked is said,
	// because no run had confirmed it when this was written (ADR-0006,
	// "unverified until done"), and the first live init is where it is
	// written down.
	app, err := s.client.ConvertAppManifest(code, "")
	var e *github.Error
	switch {
	case err == nil:
		fmt.Fprintln(os.Stderr, "the conversion endpoint accepted the request without a token")
	case errors.As(err, &e) && (e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden):
		refused := e.Status
		app, err = s.client.ConvertAppManifest(code, s.token)
		if err == nil {
			fmt.Fprintf(os.Stderr, "the conversion endpoint needed the setup token (a %d without one)\n", refused)
		}
	}
	if err != nil {
		if unreachable(err) {
			return unreachableExit(err)
		}
		fmt.Fprintf(os.Stderr, "init: could not convert the manifest code into an App: %v\n", err)
		fmt.Fprintln(os.Stderr, "the code is good for one hour and for one conversion; run init again to register the App, or register it by hand (README step 3)")
		fmt.Fprintln(os.Stderr, "stopped at step 3; what was done before it stands, and a second run carries on from here")
		return 1
	}
	if app.ID == 0 || app.PEM == "" {
		fmt.Fprintln(os.Stderr, "init: the conversion answered without an App ID or a private key; nothing was stored")
		fmt.Fprintln(os.Stderr, "stopped at step 3; what was done before it stands, and a second run carries on from here")
		return 1
	}
	appID := strconv.FormatInt(app.ID, 10)
	// The key, as GitHub sent it: from here to the sealed box and to the
	// signature below, and nowhere else.
	pem := []byte(app.PEM)
	registered := app.Name
	if registered == "" {
		registered = app.Slug
	}
	fmt.Fprintf(os.Stderr, "the App %s (ID %s) is registered at %s; storing its two secrets\n", registered, appID, app.HTMLURL)

	// The two secrets, through #11's seam: the ID as decimal digits, the
	// key exactly as received. Stored BEFORE the install page opens, so a
	// person who closes the laptop here has the credential in the
	// repository and only a click left.
	if rc := s.sealApp(appID, pem); rc != 0 {
		return rc
	}

	// The installation is still a click in a browser. The page is opened,
	// the person is told what to press, and GET /repos/{o}/{r}/installation
	// — which only a JWT signed with the App's own key can ask — is polled
	// until it answers 200.
	installURL := appmanifest.InstallURL(serverURL, app.Slug)
	if app.Slug == "" && app.HTMLURL != "" {
		// An App's html_url is https://<server>/apps/<slug>; an answer
		// without a slug still carries that.
		installURL = strings.TrimRight(app.HTMLURL, "/") + "/installations/new"
	}
	openBrowser(installURL, s.flags.noBrowser)
	fmt.Fprintf(os.Stderr, "click \"Install\", then \"Only select repositories\", and pick %s (waiting up to %s for the installation)\n", repository, wait)
	deadline := time.Now().Add(timeout)
	for {
		jwt, err := github.AppJWT(appID, pem, time.Now())
		if err != nil {
			// GitHub issued a key this cannot sign with. The secrets are
			// stored as received; the install is a person's to make and
			// doctor's to check.
			fmt.Fprintf(os.Stderr, "init: cannot sign a JWT with the App's key: %v\n", err)
			return notInstalled(s, installURL, repository, "could not sign a JWT with the App's key — install it at "+installURL+", then run falconet doctor")
		}
		_, err = s.client.GetInstallation(s.owner, s.name, jwt)
		if err == nil {
			s.say(doctor.Line{Status: doctor.Done, Step: 3,
				Text: fmt.Sprintf("the GitHub App %s (ID %s) is registered, installed on %s, and its two secrets are stored", registered, appID, repository)})
			return 0
		}
		var e *github.Error
		if errors.As(err, &e) && e.Status != http.StatusNotFound {
			// Not "not yet": GitHub refused the question itself, and
			// asking again would not change the answer.
			fmt.Fprintf(os.Stderr, "init: %v\n", err)
			return notInstalled(s, installURL, repository, fmt.Sprintf("%d %s — install it at %s, then run falconet doctor", e.Status, e.Reason(), installURL))
		}
		if time.Now().Add(installPoll).After(deadline) {
			return notInstalled(s, installURL, repository, fmt.Sprintf("timed out after %s — install it at %s, then run falconet doctor", wait, installURL))
		}
		time.Sleep(installPoll)
	}
}

// waitWord is a duration as a person reads one: "10m", not "10m0s".
func waitWord(d time.Duration) string {
	w := d.String()
	for _, zero := range []string{"0s", "0m"} {
		if len(w) > len(zero) && strings.HasSuffix(w, zero) {
			w = strings.TrimSuffix(w, zero)
		}
	}
	return w
}

// unregistered is step 3 ending before an App exists: nothing was stored,
// nothing needs undoing, and the step is left for a person — a second run,
// or the README's by-hand path.
func unregistered(s appStep, why string) int {
	fmt.Fprintf(os.Stderr, "init: step 3 — %s; the App is left for you: run init again, or register it by hand (README step 3)\n", why)
	s.say(doctor.Line{Status: doctor.Skipped, Step: 3,
		Text: "secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (the App was not registered: " + why + ")"})
	s.left.add(leftApp, setup.LeftApp)
	return 0
}

// notInstalled is the App registered and its secrets stored, with the
// installation not confirmed: the run goes on, since the install is a click
// a person can make later, and doctor is what checks it.
func notInstalled(s appStep, installURL, repository, why string) int {
	s.say(doctor.CannotTellWhy(3, "the App is installed", why))
	s.left.add(leftApp, setup.LeftInstall(installURL, repository))
	return 0
}

// openBrowser sends the person's browser to target — `open` on macOS,
// `xdg-open` on Linux, as a subprocess whose output is not this verb's to
// print — and says so. With --no-browser, or where neither exists, the URL
// is printed for the person to open; the line is the same either way, so
// a test drives the flow by reading it.
func openBrowser(target string, noBrowser bool) {
	if !noBrowser {
		if opener := browserOpener(); opener != "" {
			cmd := exec.Command(opener, target)
			if err := cmd.Start(); err == nil {
				go func() { _ = cmd.Wait() }()
				fmt.Fprintf(os.Stderr, "opening %s in a browser (if nothing appears, open it yourself)\n", target)
				return
			}
		}
	}
	fmt.Fprintf(os.Stderr, "open this in a browser: %s\n", target)
}

// browserOpener is the program that opens a URL on this platform, by
// absolute path, or empty where there is none.
func browserOpener() string {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return ""
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}
