package main

// init — install falconet into the repository this is run in: README steps
// 2–8, each idempotent, each reported one line in doctor's format, then one
// commit and never a push. What each step decides and writes is
// internal/setup, which carries the record; doctor's checks run through
// internal/doctor so the two verbs are the same code; the plan-env shape is
// internal/planenv and the sealed box is internal/secrets. This file is the
// flags, the repository, the prompts, git, the API calls, and the exit code.
//
// The second setup verb (ADR-0006 D4, D5): a person invokes it, on a laptop,
// in a clone of the repository they are installing falconet into. Its order
// is the record's: every read before any write, and the first write is the
// idempotent one — the labels — so a token short of a permission fails
// there, before anything harder to undo, with the permission named.
//
// Without a token it still does the local steps — the .gitignore line, the
// config, the workflow file — and prints what is left. It degrades to the
// README, never to nothing.

import (
	"bufio"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/zetlen/falconet/internal/appmanifest"
	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/doctor"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/planenv"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/secrets"
	"github.com/zetlen/falconet/internal/setup"
	"github.com/zetlen/falconet/prompts"
)

const initUsageText = `init — install falconet into this repository: README steps 2–8, each
idempotent and reported one line, then one commit, never a push.

Modes:
  falconet init [--repo owner/name] [--config FILE]
                [--plan a,b] [--validate-only c,d]
                [--plan-env-file FILE]
                [--app-name NAME] [--app-timeout 10m] [--no-browser] [--no-app]
                [--app-id N --app-key FILE]
                [--replace-secrets]
                [--no-commit]

    --repo             the GitHub repository, instead of $GITHUB_REPOSITORY
                       or the origin remote of the clone this runs in
    --config           the config file, instead of .github/falconet.json
    --plan             the stacks a human will apply from the pull request
    --validate-only    every other directory with .tf in it. Both are
                       comma-separated, and every discovered stack must be
                       named in one of them — unless stdin is a terminal,
                       where init asks per stack
    --plan-env-file    a JSON object of the variables the planned stacks
                       need, kept OUTSIDE the repository; sealed as
                       FALCONET_PLAN_ENV (step 5)
    --app-name         the name of the App init registers (default
                       falconet-<owner>-<repo>, cut to GitHub's 34)
    --app-timeout      how long to wait for you and a browser, twice: for
                       GitHub's redirect, then for the installation
                       (default 10m)
    --no-browser       print each URL instead of opening it
    --no-app           do not register an App; step 3 is left for you
    --app-id N         with --app-key, an App registered by hand (README
    --app-key FILE     step 3), sealed as FALCONET_APP_ID and
                       FALCONET_APP_PRIVATE_KEY instead of registering one
    --replace-secrets  seal a new value over a secret that already exists;
                       without it an existing secret is left as it is
    --no-commit        leave what was written staged, and say so

Runs from the root of the repository it is standing in, which must have a
clean working tree: the commit this makes carries only what it wrote.

Order (ADR-0006 D4): every read before any write, and the first write is
the idempotent one. The labels (6), then the secrets (4, 5, then 3), then
the local files (2, 7, 8), then one commit. A token short of a permission
fails at the labels, before anything harder to undo; a 403 names it.

Step 3, the App, is done by manifest (ADR-0006 D5): init serves a page on
localhost that sends the App's configuration — the three repository
permissions, no webhook, installable only on this account — to GitHub,
where you click "Create GitHub App"; GitHub sends the browser back with a
code, and the code is exchanged for the App's ID and private key, which go
straight into sealed boxes and into the repository's secrets. The key is
never written to disk. Then the App's install page opens, and init waits
for you to install it on this repository.

ANTHROPIC_API_KEY is read from a no-echo prompt when stdin is a terminal,
and from stdin otherwise — never from an argument, which would be in shell
history. Empty is skipped. FALCONET_PLAN_ENV comes from --plan-env-file,
and is refused unless it parses as a JSON object whose values are all
strings and whose keys are variable names; the value is never echoed.

With FALCONET_SETUP_TOKEN — a fine-grained personal access token scoped to
the one repository, with Administration and Actions read, Secrets and
Issues write (a classic token needs repo) — the remote steps run; without
it they are skipped and listed under "Left for you:" in the README's
words. GITHUB_TOKEN and GH_TOKEN are deliberately not read. GITHUB_API_URL
overrides the API endpoint.

Prints one line per step on stdout, in doctor's format — a fixed-width
status word, the step number and the step:

  ok           already done; nothing written
  done         written, created or stored by this run
  skipped      not attempted, and listed under "Left for you:"
  MISSING      a check doctor makes that init cannot fix
  cannot tell  a check that could not run, and why
  note         not a step: something the README says in a sentence

then a summary line, then "Left for you:" — what remains, in order, ending
with the canary and the check (falconet doctor).

Exit codes: 0 = everything attempted succeeded (skipped steps are not
                failures; neither is a MISSING init cannot fix)
            1 = a dirty tree, a refused write, a refused plan-env, a
                repository that does not qualify or cannot be reached
            2 = usage error (including --help)
`

func initUsage() int {
	fmt.Fprint(os.Stderr, initUsageText)
	return 2
}

// initFlags is the command line, read.
type initFlags struct {
	repo, explicit, plan, validateOnly, planEnvFile, appID, appKey string
	appName, appTimeout                                            string
	planGiven, validateGiven, replace, noCommit, noApp, noBrowser  bool
	// timeout is --app-timeout, parsed.
	timeout time.Duration
}

func runInit(args []string) int {
	var f initFlags
	for len(args) > 0 {
		flag := args[0]
		value := func(what string) (string, bool) {
			if len(args) < 2 || args[1] == "" {
				fmt.Fprintf(os.Stderr, "%s needs %s\n", flag, what)
				return "", false
			}
			return args[1], true
		}
		var v string
		ok := true
		takes := true
		switch flag {
		case "--repo":
			v, ok = value("owner/name")
			f.repo = v
		case "--config":
			v, ok = value("a file")
			f.explicit = v
		case "--plan":
			v, ok = value("stack names, comma-separated")
			f.plan, f.planGiven = v, true
		case "--validate-only":
			v, ok = value("stack names, comma-separated")
			f.validateOnly, f.validateGiven = v, true
		case "--plan-env-file":
			v, ok = value("a file")
			f.planEnvFile = v
		case "--app-id":
			v, ok = value("the App ID")
			f.appID = v
		case "--app-key":
			v, ok = value("the App's private key, a .pem file")
			f.appKey = v
		case "--app-name":
			v, ok = value("a name for the App")
			f.appName = v
		case "--app-timeout":
			v, ok = value("a duration, like 10m")
			f.appTimeout = v
		case "--no-app":
			f.noApp, takes = true, false
		case "--no-browser":
			f.noBrowser, takes = true, false
		case "--replace-secrets":
			f.replace, takes = true, false
		case "--no-commit":
			f.noCommit, takes = true, false
		case "-h", "--help":
			return initUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return initUsage()
		}
		if !ok {
			return 2
		}
		if takes {
			args = args[2:]
		} else {
			args = args[1:]
		}
	}
	if (f.appID == "") != (f.appKey == "") {
		fmt.Fprintln(os.Stderr, "--app-id and --app-key go together: the App's ID and its private key are two halves of one credential")
		return 2
	}
	if f.appID != "" && !digits.MatchString(f.appID) {
		fmt.Fprintln(os.Stderr, "--app-id must be a number (the App ID near the top of the App's page)")
		return 2
	}
	// An App name GitHub would refuse is refused here, before a browser
	// opens on it.
	if _, _, err := appmanifest.Name(f.appName, "", ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	f.timeout = appTimeoutDefault
	if f.appTimeout != "" {
		d, err := time.ParseDuration(f.appTimeout)
		if err != nil || d <= 0 {
			fmt.Fprintf(os.Stderr, "--app-timeout must be a positive duration, like 10m or 90s (got %q)\n", f.appTimeout)
			return 2
		}
		f.timeout = d
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}
	// Every file the caller names resolves against where the caller stands,
	// before the cd to the root; config resolves at the root after it.
	for _, p := range []*string{&f.explicit, &f.planEnvFile, &f.appKey} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(cwd, *p)
		}
	}
	root, err := repo.Root(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot enter %s: %v\n", root, err)
		return 1
	}

	// The report is printed as it happens — a line lands when its step does,
	// so a person at a prompt sees what came before — and kept for the
	// summary.
	var report doctor.Report
	say := func(l doctor.Line) {
		report = append(report, l)
		fmt.Println(l.String())
	}
	// What is left for a person, collected as the run goes and printed in
	// the README's order at the end: the push first, then the steps init
	// skipped (3–7), then what doctor found wrong and init cannot fix, then
	// the canary and the check.
	var left leftovers

	// --- preflight: the tree ----------------------------------------------
	//
	// The tree must be clean before anything else happens, and refused before
	// any call and before any file: the commit this verb makes must carry only
	// what it wrote, which is the same reason prepare refuses a dirty tree
	// before the agent runs — a commit that carries what the tree happened to
	// hold is a reading of the tree that is a lie.
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "init: %s is not a git repository\n", root)
		return 1
	}
	status := exec.Command("git", "status", "--porcelain")
	status.Stderr = os.Stderr
	dirt, err := status.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "init: could not read the working tree")
		return 1
	}
	if len(strings.TrimSpace(string(dirt))) > 0 {
		fmt.Fprintln(os.Stderr, "init: the working tree is dirty, and the commit init makes must carry only what it writes; commit or stash these first:")
		fmt.Fprint(os.Stderr, string(dirt))
		return 1
	}
	say(doctor.Line{Status: doctor.OK, Step: 1, Text: "the working tree is clean"})

	// --- preflight: the config ----------------------------------------------
	//
	// A config that exists and does not parse is a refusal, not a MISSING:
	// init never rewrites a consumer's config, and every later step — the
	// label names, the handoff directory, the stacks — reads it.
	cfg, err := config.Load(f.explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		fmt.Fprintln(os.Stderr, "init never rewrites a config: fix it, or move it aside to have one written")
		return 1
	}

	// --- preflight: the inputs that are files ---------------------------------
	//
	// Validated before any write, so a malformed file costs nothing. The
	// plan-env value is a credential: the error names the shape and never the
	// value (internal/planenv), and nothing here prints the bytes.
	var planEnv []byte
	if f.planEnvFile != "" {
		planEnv, err = os.ReadFile(f.planEnvFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot read --plan-env-file: %v\n", err)
			return 1
		}
		if _, err := planenv.Parse(planEnv); err != nil {
			fmt.Fprintf(os.Stderr, "init: validation: %v\n", err)
			return 1
		}
	}
	var appKey []byte
	if f.appKey != "" {
		appKey, err = os.ReadFile(f.appKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot read --app-key: %v\n", err)
			return 1
		}
		// The whole PEM, header and footer lines included (README step 3):
		// a file that is not a PEM private key is the wrong file, and is
		// refused before it is sealed into a secret a run would then fail on.
		block, _ := pem.Decode(appKey)
		if block == nil || !strings.HasSuffix(block.Type, "PRIVATE KEY") {
			fmt.Fprintln(os.Stderr, "init: --app-key is not a PEM private key (the .pem GitHub downloads when you generate one)")
			return 1
		}
	}

	// --- preflight: the stacks -----------------------------------------------
	//
	// Decided now, before any write, because step 5 needs to know whether any
	// stack is planned and step 7 needs the lists. A config that exists is
	// the answer and is kept; otherwise the stacks are discovered and the
	// flags sort them, or the prompt does.
	terminal := isTerminal()
	stdin := bufio.NewReader(os.Stdin)
	writeConfig := cfg.File == ""
	var stacks setup.Stacks
	if writeConfig {
		discovered, err := setup.DiscoverStacks(os.DirFS(root))
		if err != nil {
			fmt.Fprintf(os.Stderr, "init: looking for stacks: %v\n", err)
			return 1
		}
		if len(discovered) == 0 {
			fmt.Fprintln(os.Stderr, "init: this repository does not qualify: no directory under it holds a .tf file (README step 1:")
			fmt.Fprintln(os.Stderr, "OpenTofu, with each stack in its own subdirectory — a .tf at the root is not a stack)")
			return 1
		}
		stacks, err = setup.Sort(discovered, setup.SplitList(f.plan), setup.SplitList(f.validateOnly))
		var unknown *setup.UnknownStackError
		var unsorted *setup.UnsortedError
		switch {
		case errors.As(err, &unknown):
			fmt.Fprintf(os.Stderr, "init: %v\n", err)
			return 2
		case errors.As(err, &unsorted) && terminal:
			stacks, err = askStacks(stdin, unsorted.Names, setup.SplitList(f.plan), setup.SplitList(f.validateOnly), discovered)
			if err != nil {
				fmt.Fprintf(os.Stderr, "init: %v\n", err)
				return 1
			}
		case errors.As(err, &unsorted):
			// Not at a terminal and named in neither flag: validate_only,
			// which is the README's own rule — "validate_only is every other
			// directory with .tf in it" — and the safe reading, since a
			// validate-only stack is never planned and needs no credential.
			// Said per stack, so a person who meant --plan sees it.
			for _, s := range unsorted.Names {
				say(doctor.Line{Status: doctor.Note, Step: 7,
					Text: fmt.Sprintf("stack %s is named in neither --plan nor --validate-only: validate_only, the README's rule for every other directory with .tf in it", s)})
			}
			stacks, err = setup.Sort(discovered, setup.SplitList(f.plan), append(setup.SplitList(f.validateOnly), unsorted.Names...))
			if err != nil {
				fmt.Fprintf(os.Stderr, "init: %v\n", err)
				return 1
			}
		case err != nil:
			fmt.Fprintf(os.Stderr, "init: %v\n", err)
			return 1
		}
	} else {
		stacks = setup.Stacks{Plan: cfg.Schema.Stacks.Plan, ValidateOnly: cfg.Schema.Stacks.ValidateOnly}
	}
	planned := len(stacks.Plan)

	// --- which repository ----------------------------------------------------
	//
	// --repo, else $GITHUB_REPOSITORY, else the origin remote, as doctor
	// decides it. Needed for every call; without a token there are no calls,
	// and the local steps do not need to know.
	token := github.SetupTokenFromEnv()
	var owner, name string
	if f.repo != "" {
		owner, name, err = github.SplitRepository(f.repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "--repo %v\n", err)
			return 2
		}
	} else {
		owner, name, err = repo.Repository(root)
		if err != nil && token != "" {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
	}

	// --- the remote reads, doctor's, every one before any write ------------
	//
	// The repository must answer: a 404 is "not found, or no access", and
	// nothing can be written to a repository the token cannot see. has_issues
	// and the Actions policy are reported in doctor's words and do not stop
	// the run — init cannot change either, so they land under "Left for
	// you:". The secrets and labels lists are what the writes below are
	// decided against; a refusal of either is a refusal of its step, after
	// the step before it.
	var (
		client                          *github.Client
		repository                      github.Repository
		existingSecrets, existingLabels map[string]bool
		errSecrets, errLabels           error
		promptedKey                     string
		anthropicFrom                   string
	)
	if token == "" {
		fmt.Fprint(os.Stderr, doctor.TokenHintFor("init", "steps 3–6 are left for you"))
		say(doctor.NoToken(1, "the repository, its issues, its Actions policy, its secrets and its labels"))
	} else {
		client = github.New(github.APIURLFromEnv(), token)
		header, err := client.Request("GET", "/repos/"+owner+"/"+name, nil, &repository)
		if header != nil {
			if note, has := doctor.ClassicToken(header.Get("X-OAuth-Scopes")); has {
				say(note)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "init: the repository %s/%s did not answer: %v\n", owner, name, err)
			if isHTTP(err) {
				fmt.Fprintln(os.Stderr, "nothing can be installed into a repository the token cannot see; check the name and the token's repository access")
			}
			return 1
		}
		say(doctor.Issues(&repository))
		if last := report[len(report)-1]; last.Status == doctor.Missing {
			left.add(leftFix, setup.LeftFix(last))
		}

		permissions, errPerm := client.GetActionsPermissions(owner, name)
		if unreachable(errPerm) {
			return unreachableExit(errPerm)
		}
		var selected *github.SelectedActions
		var errSel error
		if errPerm == nil && permissions.AllowedActions == "selected" {
			selected, errSel = client.GetSelectedActions(owner, name)
			if unreachable(errSel) {
				return unreachableExit(errSel)
			}
		}
		switch {
		case errPerm != nil:
			say(doctor.Refused(1, "allowed_actions", errPerm, doctor.NeedsAdministration))
		case errSel != nil:
			say(doctor.Refused(1, "allowed_actions is selected", errSel, doctor.NeedsAdministration))
		default:
			say(doctor.ActionsPolicy(permissions, selected))
		}
		if last := report[len(report)-1]; last.Status == doctor.Missing {
			left.add(leftFix, setup.LeftFix(last))
		}
		workflowPerm, errWorkflow := client.GetWorkflowPermissions(owner, name)
		if unreachable(errWorkflow) {
			return unreachableExit(errWorkflow)
		}
		if errWorkflow == nil {
			say(doctor.WorkflowPermissionsNote(workflowPerm))
		}

		secretList, err := client.ListSecrets(owner, name)
		if unreachable(err) {
			return unreachableExit(err)
		}
		errSecrets = err
		existingSecrets = map[string]bool{}
		for _, s := range secretList {
			existingSecrets[s.Name] = true
		}
		labelList, err := client.ListLabels(owner, name)
		if unreachable(err) {
			return unreachableExit(err)
		}
		errLabels = err
		existingLabels = map[string]bool{}
		for _, l := range labelList {
			existingLabels[l.Name] = true
		}

		// --- the inputs that are prompts: after the reads, before the writes
		//
		// The API key is asked for only when it will be stored — an existing
		// secret is not replaced without --replace-secrets — and never from
		// an argument. On a terminal, with echo off; otherwise from stdin,
		// which is how a key is piped in. One trailing newline is dropped:
		// the Enter that ended the prompt, or the one `echo` adds.
		if errSecrets == nil && (!existingSecrets["ANTHROPIC_API_KEY"] || f.replace) {
			if terminal {
				anthropicFrom = "nothing entered"
				promptedKey, err = readHidden(stdin, "ANTHROPIC_API_KEY (an API key from the Anthropic console; input hidden; empty to skip): ")
			} else {
				anthropicFrom = "nothing on stdin"
				var raw []byte
				raw, err = io.ReadAll(stdin)
				promptedKey = string(raw)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "init: reading ANTHROPIC_API_KEY: %v\n", err)
				return 1
			}
			promptedKey = strings.TrimSuffix(strings.TrimSuffix(promptedKey, "\n"), "\r")
		}
		// The plan-env path, asked for once on a terminal when the flag did
		// not name it and a stack is planned; an empty answer skips.
		if errSecrets == nil && terminal && f.planEnvFile == "" && planned > 0 && (!existingSecrets["FALCONET_PLAN_ENV"] || f.replace) {
			fmt.Fprint(os.Stderr, "FALCONET_PLAN_ENV: path to a JSON object of the variables the planned stacks need (kept outside the repository; empty to skip): ")
			line, err := stdin.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				fmt.Fprintf(os.Stderr, "init: reading the path: %v\n", err)
				return 1
			}
			if path := strings.TrimSpace(line); path != "" {
				if !filepath.IsAbs(path) {
					path = filepath.Join(cwd, path)
				}
				planEnv, err = os.ReadFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "init: cannot read %s: %v\n", path, err)
					return 1
				}
				if _, err := planenv.Parse(planEnv); err != nil {
					fmt.Fprintf(os.Stderr, "init: validation: %v\n", err)
					return 1
				}
				f.planEnvFile = path
			}
		}
	}

	// --- step 6: the labels, the first write ----------------------------------
	//
	// Deliberately first: idempotent and harmless, so a token short of a
	// permission fails here, before anything harder to undo. A 403 names the
	// permission and stops. A 422 is GitHub saying the label exists — one
	// the list did not carry, or a race — which is the state wanted.
	want := doctor.Labels{
		Queue:     cfg.Schema.Issue.QueueLabel,
		NeedsInfo: cfg.Schema.Labels.NeedsInfo,
		Human:     cfg.Schema.Labels.Human,
		PR:        cfg.Schema.Labels.PR,
	}
	switch {
	case token == "":
		say(doctor.Line{Status: doctor.Skipped, Step: 6, Text: "the four labels (no FALCONET_SETUP_TOKEN)"})
		left.add(leftLabels, setup.LeftLabels)
	case errLabels != nil:
		say(doctor.Refused(6, "the four labels", errLabels, needsIssuesWrite))
		fmt.Fprintf(os.Stderr, "init: cannot read the labels, so cannot create them: %v\n", errLabels)
		return 1
	default:
		for _, step := range setup.Labels(want, sortedKeys(existingLabels)) {
			if step.Create == nil {
				say(doctor.Line{Status: doctor.OK, Step: 6, Text: "label " + step.Name + " exists"})
				continue
			}
			err := client.CreateLabel(owner, name, *step.Create)
			var e *github.Error
			switch {
			case err == nil:
				say(doctor.Line{Status: doctor.Done, Step: 6, Text: "label " + step.Name + " created"})
			case errors.As(err, &e) && e.Status == http.StatusUnprocessableEntity:
				say(doctor.Line{Status: doctor.OK, Step: 6, Text: "label " + step.Name + " exists"})
			default:
				return refusedWrite(6, "could not create label "+step.Name, err, needsIssuesWrite)
			}
		}
	}

	// --- steps 4, 5 and 3: the secrets ------------------------------------------
	//
	// The public key is fetched once, when the first value is to be sealed,
	// and a 403 on it or on a PUT names Secrets: write and stops — after the
	// labels, before the local files. A secret that exists is left alone
	// unless --replace-secrets; a value can never be read back, so the name
	// is the check, as doctor says.
	switch {
	case token == "":
		say(doctor.Line{Status: doctor.Skipped, Step: 4, Text: "secret ANTHROPIC_API_KEY (no FALCONET_SETUP_TOKEN)"})
		left.add(leftAnthropic, setup.LeftAnthropic)
		if planned == 0 {
			say(doctor.SecretLine(doctor.Secret{Name: "FALCONET_PLAN_ENV", Step: 5}, nil, planned, true))
		} else {
			say(doctor.Line{Status: doctor.Skipped, Step: 5, Text: "secret FALCONET_PLAN_ENV (no FALCONET_SETUP_TOKEN)"})
			left.add(leftPlanEnv, setup.LeftPlanEnv)
		}
		say(doctor.Line{Status: doctor.Skipped, Step: 3, Text: "secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (no FALCONET_SETUP_TOKEN)"})
		left.add(leftApp, setup.LeftApp)
	case errSecrets != nil:
		say(doctor.Refused(3, "the secrets", errSecrets, needsSecretsWrite))
		fmt.Fprintf(os.Stderr, "init: cannot read the secrets, so cannot store them: %v\n", errSecrets)
		return 1
	default:
		var publicKey []byte
		var keyID string
		// store seals value as the secret called secretName, fetching the
		// public key on first use. rc is non-zero when the run must stop.
		store := func(step int, secretName string, value []byte) (rc int) {
			if publicKey == nil {
				pk, err := client.GetSecretsPublicKey(owner, name)
				if err != nil {
					return refusedWrite(step, "could not read the repository's secrets public key", err, needsSecretsWrite)
				}
				publicKey, err = secrets.DecodeKey(pk.Key)
				if err != nil {
					fmt.Fprintf(os.Stderr, "init: %v\n", err)
					return 1
				}
				keyID = pk.KeyID
			}
			sealed, err := secrets.Seal(publicKey, value)
			if err != nil {
				fmt.Fprintf(os.Stderr, "init: %v\n", err)
				return 1
			}
			if err := client.PutSecret(owner, name, secretName, sealed, keyID); err != nil {
				return refusedWrite(step, "could not store secret "+secretName, err, needsSecretsWrite)
			}
			what := "stored"
			if existingSecrets[secretName] {
				what = "replaced"
			}
			say(doctor.Line{Status: doctor.Done, Step: step, Text: "secret " + secretName + " " + what + " (sealed to key " + keyID + ")"})
			return 0
		}
		exists := func(step int, secretName string) {
			say(doctor.Line{Status: doctor.OK, Step: step, Text: "secret " + secretName + " exists (not replaced; --replace-secrets would)"})
		}

		// Step 4.
		switch {
		case existingSecrets["ANTHROPIC_API_KEY"] && !f.replace:
			exists(4, "ANTHROPIC_API_KEY")
		case promptedKey == "":
			say(doctor.Line{Status: doctor.Skipped, Step: 4, Text: "secret ANTHROPIC_API_KEY (" + anthropicFrom + ")"})
			left.add(leftAnthropic, setup.LeftAnthropic)
		default:
			if rc := store(4, "ANTHROPIC_API_KEY", []byte(promptedKey)); rc != 0 {
				return rc
			}
		}
		// Step 5.
		switch {
		case existingSecrets["FALCONET_PLAN_ENV"] && !f.replace:
			exists(5, "FALCONET_PLAN_ENV")
		case f.planEnvFile == "" && planned == 0:
			say(doctor.SecretLine(doctor.Secret{Name: "FALCONET_PLAN_ENV", Step: 5}, nil, planned, true))
		case f.planEnvFile == "":
			say(doctor.Line{Status: doctor.Skipped, Step: 5, Text: "secret FALCONET_PLAN_ENV (no --plan-env-file)"})
			left.add(leftPlanEnv, setup.LeftPlanEnv)
		default:
			if rc := store(5, "FALCONET_PLAN_ENV", planEnv); rc != 0 {
				return rc
			}
		}
		// Step 3: the App. By hand from the flags when they were given — a
		// person who registered one has the credential, and the flags win;
		// else, when the two secrets are absent (or --replace-secrets), by
		// manifest from a browser (init_app.go), unless --no-app leaves the
		// step for a person. sealApp is the seam both paths share: an ID
		// and a PEM, sealed into the two secrets.
		sealApp := func(appID string, privateKey []byte) int {
			if rc := store(3, "FALCONET_APP_ID", []byte(appID)); rc != 0 {
				return rc
			}
			return store(3, "FALCONET_APP_PRIVATE_KEY", privateKey)
		}
		hasApp := existingSecrets["FALCONET_APP_ID"] && existingSecrets["FALCONET_APP_PRIVATE_KEY"]
		switch {
		case f.appID != "" && hasApp && !f.replace:
			exists(3, "FALCONET_APP_ID")
			exists(3, "FALCONET_APP_PRIVATE_KEY")
		case f.appID != "":
			if rc := sealApp(f.appID, appKey); rc != 0 {
				return rc
			}
		case hasApp && (!f.replace || f.noApp):
			exists(3, "FALCONET_APP_ID")
			exists(3, "FALCONET_APP_PRIVATE_KEY")
		case f.noApp:
			say(doctor.Line{Status: doctor.Skipped, Step: 3, Text: "secrets FALCONET_APP_ID and FALCONET_APP_PRIVATE_KEY (--no-app)"})
			left.add(leftApp, setup.LeftApp)
		default:
			if rc := registerApp(appStep{client: client, token: token, repo: &repository, owner: owner, name: name,
				flags: f, say: say, left: &left, sealApp: sealApp}); rc != 0 {
				return rc
			}
		}
	}

	// --- the local files: steps 2, 7 and 8 -----------------------------------
	//
	// Each idempotent: a file that exists is kept and reported, never
	// rewritten. What is written is remembered by path, for `git add` of
	// exactly those paths and for the commit body.
	var written []setup.Written
	write := func(path string, data []byte, perm os.FileMode, what string) int {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot create %s: %v\n", filepath.Dir(path), err)
			return 1
		}
		if err := os.WriteFile(path, data, perm); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write %s: %v\n", path, err)
			return 1
		}
		written = append(written, setup.Written{Path: path, What: what})
		return 0
	}

	// Step 2: the handoff directory is ignored. `git check-ignore` is the
	// check, as doctor makes it, so an entry in .git/info/exclude or a
	// broader pattern counts; the line is appended only when it does not.
	entry := setup.IgnoreEntry(cfg.Schema.HandoffDir)
	switch rc, _ := checkIgnore(cfg.Schema.HandoffDir); rc {
	case 0:
		say(doctor.HandoffIgnored(cfg.Schema.HandoffDir, true))
	case 1:
		current, err := os.ReadFile(".gitignore")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "init: cannot read .gitignore: %v\n", err)
			return 1
		}
		out, changed := setup.AppendIgnore(current, entry)
		if changed {
			if rc := write(".gitignore", out, 0o644, "ignores "+entry+", the handoff directory (README step 2)"); rc != 0 {
				return rc
			}
			say(doctor.Line{Status: doctor.Done, Step: 2, Text: entry + " added to .gitignore"})
		} else {
			// The line is there and git still says not ignored: something
			// later in the file un-ignores it. Not init's to untangle.
			say(doctor.HandoffIgnored(cfg.Schema.HandoffDir, false))
			left.add(leftFix, setup.LeftFix(doctor.HandoffIgnored(cfg.Schema.HandoffDir, false)))
		}
	default:
		fmt.Fprintln(os.Stderr, "init: git check-ignore failed")
		return 1
	}

	// Step 7: the config and the prompt. A config that exists is kept — and
	// checked as doctor checks it; what it names wrong is left for a person.
	// One written names the stacks and the prompt copy.
	if !writeConfig {
		say(doctor.ConfigLine(cfg.File, nil))
		var onDisk []doctor.Stack
		for _, key := range []struct {
			name  string
			names []string
		}{{"plan", cfg.Schema.Stacks.Plan}, {"validate_only", cfg.Schema.Stacks.ValidateOnly}} {
			for _, s := range key.names {
				onDisk = append(onDisk, stackOnDisk(key.name, s))
			}
		}
		for _, l := range doctor.Stacks(onDisk, cfg.File) {
			say(l)
			if l.Status == doctor.Missing {
				left.add(leftFix, setup.LeftFix(l))
			}
		}
		// The prompt overrides the file sets: one that names a missing file
		// under the root gets the shipped prompt copied there, which is the
		// hint doctor gives; the rest are reported as doctor reports them.
		namesImplement := false
		for _, p := range promptsOnDisk(cfg) {
			if p.Key == "implement" {
				namesImplement = true
			}
			line := doctor.PromptLines([]doctor.Prompt{p})[0]
			if line.Status == doctor.Missing && p.Inside && !p.Exists {
				if rc := copyPrompt(p.Key, filepath.Clean(p.Path), write); rc != 0 {
					return rc
				}
				say(doctor.Line{Status: doctor.Done, Step: 7, Text: fmt.Sprintf("prompts.%s names %s, copied from the shipped prompt", p.Key, p.Path)})
				if p.Key == "implement" {
					left.add(leftPrompt, strings.Replace(setup.LeftPrompt, setup.PromptPath, p.Path, 1))
				}
				continue
			}
			say(line)
			if line.Status == doctor.Missing {
				left.add(leftFix, setup.LeftFix(line))
			}
		}
		if !namesImplement {
			say(doctor.Line{Status: doctor.Note, Step: 7, Text: "prompts.implement is not set, so the shipped prompt is used, and its standing facts are the origin's"})
			left.add(leftPrompt, setup.LeftPromptUnset)
		}
	} else {
		if rc := write(".github/falconet.json", setup.ConfigJSON(stacks), 0o644,
			fmt.Sprintf("the stacks — plan: %s; validate_only: %s — and the prompt override (README step 7)", orNone(stacks.Plan), orNone(stacks.ValidateOnly))); rc != 0 {
			return rc
		}
		say(doctor.Line{Status: doctor.Done, Step: 7,
			Text: fmt.Sprintf(".github/falconet.json written (plan: %s; validate_only: %s)", orNone(stacks.Plan), orNone(stacks.ValidateOnly))})
		if isRegularFile(setup.PromptPath) {
			say(doctor.Line{Status: doctor.OK, Step: 7, Text: "prompts.implement names " + setup.PromptPath + ", which exists"})
		} else {
			if rc := copyPrompt("implement", setup.PromptPath, write); rc != 0 {
				return rc
			}
			say(doctor.Line{Status: doctor.Done, Step: 7, Text: "prompts.implement names " + setup.PromptPath + ", copied from the shipped prompt"})
			left.add(leftPrompt, setup.LeftPrompt)
		}
	}

	// Step 8: the caller workflow, pinned to this binary's own version. One
	// that exists is kept and checked as doctor checks it.
	ref := setup.WorkflowRef(resolvedVersion())
	if text, err := os.ReadFile(doctor.WorkflowPath); err == nil {
		for _, l := range doctor.WorkflowLines(text, true) {
			say(l)
			if l.Status == doctor.Missing {
				left.add(leftFix, setup.LeftFix(l))
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if rc := write(doctor.WorkflowPath, setup.Workflow(ref), 0o644, "the caller workflow, "+setup.Uses(ref)+" (README step 8)"); rc != 0 {
			return rc
		}
		say(doctor.Line{Status: doctor.Done, Step: 8, Text: doctor.WorkflowPath + " written (uses " + setup.Uses(ref) + ")"})
	} else {
		fmt.Fprintf(os.Stderr, "init: cannot read %s: %v\n", doctor.WorkflowPath, err)
		return 1
	}

	// --- the commit, never the push ------------------------------------------
	//
	// Exactly the paths written, never -A: the tree was clean, so nothing
	// else could be staged, but the pathspec is the statement. The message
	// goes through a file, as every commit in this pipeline does, so no
	// shell is asked to quote it.
	branch := currentBranch()
	switch {
	case len(written) == 0:
		say(doctor.Line{Status: doctor.OK, Text: "nothing to commit: every file exists"})
	default:
		paths := make([]string, 0, len(written))
		for _, w := range written {
			paths = append(paths, w.Path)
		}
		add := exec.Command("git", append([]string{"add", "--"}, paths...)...)
		add.Stdout, add.Stderr = os.Stderr, os.Stderr
		if err := add.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "init: git add failed")
			return 1
		}
		if f.noCommit {
			say(doctor.Line{Status: doctor.Skipped, Text: fmt.Sprintf("the commit (--no-commit): %d %s staged", len(paths), files(len(paths)))})
			left.add(leftPush, setup.LeftCommit(branch))
			break
		}
		msg, err := os.CreateTemp("", "falconet-init-*.txt")
		if err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write the commit message: %v\n", err)
			return 1
		}
		defer func() { _ = os.Remove(msg.Name()) }()
		if _, err := msg.Write(setup.CommitMessage(written)); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write the commit message: %v\n", err)
			return 1
		}
		if err := msg.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "init: cannot write the commit message: %v\n", err)
			return 1
		}
		commit := exec.Command("git", "commit", "-q", "-F", msg.Name())
		commit.Stdout, commit.Stderr = os.Stderr, os.Stderr
		if err := commit.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "init: git commit failed; the files are written and staged")
			return 1
		}
		say(doctor.Line{Status: doctor.Done, Text: fmt.Sprintf("committed %q (%d %s)", setup.CommitSubject, len(paths), files(len(paths)))})
		left.add(leftPush, setup.LeftPush(branch))
	}

	fmt.Println(setup.Summary(report))
	left.add(leftCanary, setup.LeftCanary)
	left.add(leftDoctor, setup.LeftDoctor)
	fmt.Print(setup.LeftForYou(left.inOrder()))
	return 0
}

// leftovers is the "Left for you:" list, each item with its place in the
// README's order; items are added as the run meets them and sorted once.
type leftovers []struct {
	order int
	text  string
}

const (
	leftPush = iota
	leftApp
	leftAnthropic
	leftPlanEnv
	leftLabels
	leftPrompt
	leftFix
	leftCanary
	leftDoctor
)

func (l *leftovers) add(order int, text string) {
	*l = append(*l, struct {
		order int
		text  string
	}{order, text})
}

func (l leftovers) inOrder() []string {
	sort.SliceStable(l, func(i, j int) bool { return l[i].order < l[j].order })
	out := make([]string, 0, len(l))
	for _, item := range l {
		out = append(out, item.text)
	}
	return out
}

// The fine-grained permission each write needs, named in a refusal. init
// needs write where doctor needs read, and write includes read, so the
// write level is what a refused read is told to add as well.
const (
	needsIssuesWrite  = "Issues: write"
	needsSecretsWrite = "Secrets: write"
)

// refusedWrite is a write GitHub refused: the line, the error, and exit 1.
// A 403 names the permission the token is short of.
func refusedWrite(step int, what string, err error, needs string) int {
	var e *github.Error
	if errors.As(err, &e) && (e.Status == http.StatusForbidden || e.Status == http.StatusNotFound) {
		fmt.Fprintf(os.Stderr, "init: %s: %v — the token needs %s\n", what, err, needs)
	} else {
		fmt.Fprintf(os.Stderr, "init: %s: %v\n", what, err)
	}
	fmt.Fprintf(os.Stderr, "stopped at step %d; what was done before it stands, and a second run carries on from here\n", step)
	return 1
}

// unreachable is an error that is not GitHub answering: nothing to decide
// against, and every later call would wait out the timeout to say the same.
func unreachable(err error) bool {
	return err != nil && !isHTTP(err)
}

func unreachableExit(err error) int {
	fmt.Fprintf(os.Stderr, "init: %v\n", err)
	return 1
}

// isTerminal is whether stdin is a terminal a person is typing at. `stty`
// answers it exactly — it refuses anything that is not a terminal, /dev/null
// included — and is what turns echo off below; without stty, a character
// device that is not /dev/null is the best the standard library can say.
func isTerminal() bool {
	if stty, err := exec.LookPath("stty"); err == nil {
		probe := exec.Command(stty, "-g")
		probe.Stdin = os.Stdin
		return probe.Run() == nil
	}
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

// readHidden prompts on stderr and reads one line from the terminal with
// echo off, restoring it whatever happens — on an error, and on an
// interrupt, which would otherwise leave the shell silent. There is no
// x/term (ADR-0006 D1 names one dependency, and this is not it): echo is
// turned off with `stty -echo` on the terminal stdin names, as a
// subprocess, and turned back on the same way. On a terminal where stty is
// unavailable it says so and reads with echo.
func readHidden(stdin *bufio.Reader, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	restore := echoOff()
	defer restore()
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)
	go func() {
		if _, ok := <-interrupted; ok {
			restore()
			fmt.Fprintln(os.Stderr)
			os.Exit(130)
		}
	}()
	line, err := stdin.ReadString('\n')
	fmt.Fprintln(os.Stderr) // the Enter that was not echoed
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return line, nil
}

// echoOff turns the terminal's echo off and returns what turns it back on.
func echoOff() func() {
	stty, err := exec.LookPath("stty")
	if err != nil {
		fmt.Fprintln(os.Stderr, "\ninit: stty is not available, so the key will be visible as you type")
		return func() {}
	}
	off := exec.Command(stty, "-echo")
	off.Stdin = os.Stdin
	if err := off.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "\ninit: stty -echo failed, so the key will be visible as you type")
		return func() {}
	}
	return func() {
		on := exec.Command(stty, "echo")
		on.Stdin = os.Stdin
		_ = on.Run()
	}
}

// askStacks is the terminal's way of sorting the stacks the flags did not:
// one question per stack. Answers are folded into the flags' lists and
// sorted again, so the result is what the flags would have produced.
func askStacks(stdin *bufio.Reader, unsorted, plan, validateOnly, discovered []string) (setup.Stacks, error) {
	fmt.Fprintln(os.Stderr, "README step 7: plan is every stack a human will apply from the pull request; validate-only is every other directory with .tf in it.")
	for _, s := range unsorted {
		for {
			fmt.Fprintf(os.Stderr, "%s — plan or validate-only? [p/v] ", s)
			line, err := stdin.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return setup.Stacks{}, err
			}
			answer := strings.ToLower(strings.TrimSpace(line))
			if answer == "p" || answer == "plan" {
				plan = append(plan, s)
				break
			}
			if answer == "v" || answer == "validate-only" || answer == "validate" {
				validateOnly = append(validateOnly, s)
				break
			}
			if errors.Is(err, io.EOF) {
				return setup.Stacks{}, fmt.Errorf("no answer for %s; name it in --plan or --validate-only", s)
			}
		}
	}
	return setup.Sort(discovered, plan, validateOnly)
}

// copyPrompt writes the shipped prompt called key to path, through write.
func copyPrompt(key, path string, write func(string, []byte, os.FileMode, string) int) int {
	text, ok := prompts.Read(strings.ReplaceAll(key, "_", "-"))
	if !ok {
		fmt.Fprintf(os.Stderr, "init: no shipped prompt named %s\n", key)
		return 1
	}
	return write(path, text, 0o644, "the shipped prompt, copied; its standing-facts block needs editing (README step 7)")
}

// currentBranch is `git branch --show-current`: empty on a detached HEAD.
func currentBranch() string {
	out, err := exec.Command("git", "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sortedKeys(m map[string]bool) []string {
	return doctor.SortedKeys(m)
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func files(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}
