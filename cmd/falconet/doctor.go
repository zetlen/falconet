package main

// doctor — check the repository this is run in against the README's install
// steps, read-only, and say which are missing. The checks are
// internal/doctor, which carries the record; this file is the flags, the
// repository, the files, the API calls, and the exit code.
//
// The first setup verb (ADR-0006 D4): a person invokes it, on a laptop, in
// a clone of the repository they are installing falconet into. It is the
// first consumer of the GitHub client's reads, and it writes nothing — not
// to the repository, not to GitHub.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zetlen/falconet/internal/config"
	"github.com/zetlen/falconet/internal/doctor"
	"github.com/zetlen/falconet/internal/github"
	"github.com/zetlen/falconet/internal/repo"
	"github.com/zetlen/falconet/internal/stacks"
)

const doctorUsageText = `doctor — check this repository against the README's install steps 1–8, and
say which are missing. Read-only: nothing is written anywhere.

Modes:
  falconet doctor [--config FILE] [--repo owner/name]

    --config  the config file, instead of .github/falconet.json
    --repo    the GitHub repository, instead of $GITHUB_REPOSITORY or the
              origin remote of the clone this runs in

Runs from the root of the repository it is standing in. The remote checks
need FALCONET_SETUP_TOKEN — a fine-grained personal access token scoped to
the one repository, with Administration, Actions, Secrets and Issues read
(a classic token needs repo) — and without one they say so and the local
checks still run. GITHUB_TOKEN and GH_TOKEN are deliberately not read.
GITHUB_API_URL overrides the API endpoint.

Prints one line per check on stdout, a fixed-width status word then the
step number and the check:

  ok           the check passed
  MISSING      it did not, and the next line says what to do
  cannot tell  it could not run, and the line says why
  note         not a check: something the README says in a sentence

then a summary line. stderr carries only the token hint and mechanical
errors.

Exit codes: 0 = every check is ok
            1 = at least one is MISSING or cannot tell, or the repository
                could not be determined
            2 = usage error (including --help)
`

func doctorUsage() int {
	fmt.Fprint(os.Stderr, doctorUsageText)
	return 2
}

func runDoctor(args []string) int {
	var explicit, repoFlag string
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
		var ok bool
		switch flag {
		case "--config":
			v, ok = value("a file")
			explicit = v
		case "--repo":
			v, ok = value("owner/name")
			repoFlag = v
		case "-h", "--help":
			return doctorUsage()
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", flag)
			return doctorUsage()
		}
		if !ok {
			return 2
		}
		args = args[2:]
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot determine the working directory: %v\n", err)
		return 1
	}
	// A relative --config resolves from the repository root, after the cd,
	// as it does for commit, validate and prompt (and as the bash did: cd,
	// then config_init). One rule for every verb, so that a config a person
	// confirms with doctor from a subdirectory is the config the pipeline's
	// verbs read from the same place.
	root, err := repo.Root(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
		return 1
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(os.Stderr, "falconet: cannot enter %s: %v\n", root, err)
		return 1
	}

	// Which repository. --repo, else $GITHUB_REPOSITORY, else the origin
	// remote: doctor operates on the tree it stands in, so the tree's
	// origin is the right answer, unlike pause's. Decided before anything
	// is checked, token or no token: a report about an unknown repository
	// is not a report.
	var owner, name string
	if repoFlag != "" {
		owner, name, err = github.SplitRepository(repoFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "--repo %v\n", err)
			return 2
		}
	} else {
		owner, name, err = repo.Repository(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", err)
			return 1
		}
	}

	cfg, cfgErr := config.Load(explicit)
	token := github.SetupTokenFromEnv()

	// --- the remote reads, each its own check -----------------------------
	//
	// Every probe is made regardless of the one before it: doctor never
	// stops at the first refusal, because the point of the report is to say
	// everything that is wrong at once. Only an endpoint that does not
	// answer at all stops the rest, since each of those would wait out the
	// client's timeout to say the same thing.
	var (
		repository                                                   *github.Repository
		permissions                                                  *github.ActionsPermissions
		selected                                                     *github.SelectedActions
		workflowPerm                                                 *github.WorkflowPermissions
		secrets                                                      []github.Secret
		labels                                                       []github.Label
		errRepo, errPerm, errSel, errWorkflow, errSecrets, errLabels error
		scopesNote                                                   doctor.Line
		hasScopes                                                    bool
		unreachable                                                  error
	)
	if token != "" {
		client := github.New(github.APIURLFromEnv(), token)
		// The first response carries X-OAuth-Scopes for a classic token,
		// refusal or not, so it is read through Request rather than the
		// typed wrapper.
		var r github.Repository
		header, err := client.Request("GET", github.RepoPath(owner, name, ""), nil, &r)
		if header != nil {
			scopesNote, hasScopes = doctor.ClassicToken(header.Get("X-OAuth-Scopes"))
		}
		if err == nil {
			repository = &r
		}
		errRepo = err
		probe := func(call func() error) error {
			if unreachable != nil {
				return unreachable
			}
			err := call()
			if err != nil && !isHTTP(err) {
				unreachable = err
			}
			return err
		}
		if !isHTTP(errRepo) && errRepo != nil {
			unreachable = errRepo
		}
		errPerm = probe(func() (err error) { permissions, err = client.GetActionsPermissions(owner, name); return })
		if errPerm == nil && permissions.AllowedActions == "selected" {
			errSel = probe(func() (err error) { selected, err = client.GetSelectedActions(owner, name); return })
		}
		errWorkflow = probe(func() (err error) { workflowPerm, err = client.GetWorkflowPermissions(owner, name); return })
		errSecrets = probe(func() (err error) { secrets, err = client.ListSecrets(owner, name); return })
		errLabels = probe(func() (err error) { labels, err = client.ListLabels(owner, name); return })
		if unreachable != nil {
			fmt.Fprintf(os.Stderr, "falconet: %v\n", unreachable)
		}
	} else {
		fmt.Fprint(os.Stderr, doctor.TokenHint)
	}

	// remote turns one probe's result into its line: no token, a refusal,
	// or the check itself.
	remote := func(step int, text string, err error, needs string, check func() doctor.Line) doctor.Line {
		switch {
		case token == "":
			return doctor.NoToken(step, text)
		case err != nil:
			return doctor.Refused(step, text, err, needs)
		}
		return check()
	}

	var report doctor.Report
	if hasScopes {
		report = append(report, scopesNote)
	}

	// --- step 1 --------------------------------------------------------------
	stacksKnown := cfgErr == nil
	planned := 0
	if stacksKnown {
		report = append(report, stackReport(cfg, root)...)
		planned = len(resolveLayout(cfg, root).Plan)
	} else {
		report = append(report, doctor.CannotTellWhy(1, "the configured stacks", "the config did not parse"))
	}
	report = append(report, remote(1, "the repository has issues enabled", errRepo, doctor.NeedsMetadata,
		func() doctor.Line { return doctor.Issues(repository) }))
	report = append(report, remote(1, "allowed_actions", errPerm, doctor.NeedsAdministration, func() doctor.Line {
		if permissions.AllowedActions == "selected" && errSel != nil {
			return doctor.Refused(1, "allowed_actions is selected", errSel, doctor.NeedsAdministration)
		}
		return doctor.ActionsPolicy(permissions, selected)
	}))
	report = append(report, remote(1, "default_workflow_permissions", errWorkflow, doctor.NeedsAdministration,
		func() doctor.Line { return doctor.WorkflowPermissionsNote(workflowPerm) }))
	report = append(report, doctor.RunnersNote())

	// --- step 2 --------------------------------------------------------------
	//
	// The directory comes from config, so a config that did not parse leaves
	// this unknown too: checking the default instead would be the silent fall
	// back to defaults that config.Load refuses to be.
	switch {
	case !stacksKnown:
		report = append(report, doctor.CannotTellWhy(2, "the handoff directory is gitignored", "the config did not parse"))
	case !underRoot(root, cfg.Schema.HandoffDir):
		// An absolute handoff_dir is honoured by every verb (handoff.Resolve
		// keeps it as given), and git refuses to check-ignore a path outside
		// the work tree.
		report = append(report, doctor.HandoffOutside(cfg.Schema.HandoffDir))
	default:
		handoffDir := cfg.Schema.HandoffDir
		switch rc, said := checkIgnore(handoffDir); rc {
		case 0, 1:
			report = append(report, doctor.HandoffIgnored(handoffDir, rc == 0))
		default:
			report = append(report, doctor.CannotTellWhy(2, strings.TrimSuffix(handoffDir, "/")+"/ is gitignored",
				"git check-ignore: "+said))
		}
	}

	// --- steps 3–5 -----------------------------------------------------------
	names := make([]string, 0, len(secrets))
	for _, s := range secrets {
		names = append(names, s.Name)
	}
	for _, s := range doctor.Secrets {
		report = append(report, remote(s.Step, "secret "+s.Name, errSecrets, doctor.NeedsSecrets,
			func() doctor.Line { return doctor.SecretLine(s, names, planned, stacksKnown) }))
	}

	// --- step 6 --------------------------------------------------------------
	if stacksKnown {
		want := doctor.Labels{
			Queue:     cfg.Schema.Issue.QueueLabel,
			NeedsInfo: cfg.Schema.Labels.NeedsInfo,
			Human:     cfg.Schema.Labels.Human,
			PR:        cfg.Schema.Labels.PR,
		}
		have := make([]string, 0, len(labels))
		for _, l := range labels {
			have = append(have, l.Name)
		}
		for _, label := range want.Names() {
			report = append(report, remote(6, "label "+label, errLabels, doctor.NeedsIssues,
				func() doctor.Line { return doctor.LabelLine(label, have) }))
		}
	} else {
		report = append(report, doctor.CannotTellWhy(6, "the four labels", "the config did not parse"))
	}

	// --- step 7 --------------------------------------------------------------
	file := ""
	if cfg != nil {
		file = cfg.File
	} else if explicit != "" {
		file = explicit
	}
	report = append(report, doctor.ConfigLine(file, cfgErr))
	if stacksKnown {
		report = append(report, doctor.PromptLines(promptsOnDisk(cfg))...)
	} else {
		report = append(report, doctor.CannotTellWhy(7, "the prompt overrides", "the config did not parse"))
	}

	// --- step 8 --------------------------------------------------------------
	text, err := os.ReadFile(doctor.WorkflowPath)
	report = append(report, doctor.WorkflowLines(text, err == nil)...)

	for _, l := range report {
		fmt.Println(l.String())
	}
	fmt.Println(report.Summary())
	if report.Clean() {
		return 0
	}
	return 1
}

// isHTTP is whether an error is GitHub answering, as opposed to nothing
// answering.
func isHTTP(err error) bool {
	_, ok := err.(*github.Error)
	return ok
}

// resolveLayout is what this repository does with each directory that holds
// OpenTofu, as validate and prepare resolve it: the config's two lists, or
// the root modules discovery finds when it names neither. A tree that cannot
// be walked yields an empty discovery rather than an error — doctor reports
// what it can see and never refuses to run over one unreadable directory.
func resolveLayout(cfg *config.Config, root string) stacks.Layout {
	discovered, err := stacks.Discover(os.DirFS(root))
	if err != nil {
		discovered = nil
	}
	return stacks.Resolve(discovered, cfg.Schema.Stacks.Plan, cfg.Schema.Stacks.ValidateOnly)
}

// stackReport is step 1's stack lines: each configured stack on disk, then
// the directories holding .tf that the config names in neither list (#23) —
// or, for a config that names no stacks at all, what discovery found and the
// fact that it was discovery that found it.
func stackReport(cfg *config.Config, root string) []doctor.Line {
	layout := resolveLayout(cfg, root)
	discovered, err := stacks.Discover(os.DirFS(root))
	if err != nil {
		discovered = nil
	}
	if !layout.Declared {
		return doctor.Discovered(discovered)
	}
	var onDisk []doctor.Stack
	for _, key := range []struct {
		name  string
		names []string
	}{{"plan", layout.Plan}, {"validate_only", layout.Check}} {
		for _, s := range key.names {
			onDisk = append(onDisk, stackOnDisk(key.name, s))
		}
	}
	return append(doctor.Stacks(onDisk, cfg.File),
		doctor.Undeclared(stacks.Undeclared(discovered, layout), cfg.File)...)
}

// stackOnDisk is what a configured stack name is, from the repository root.
func stackOnDisk(key, name string) doctor.Stack {
	s := doctor.Stack{Key: key, Name: name}
	info, err := os.Stat(name)
	if err != nil || !info.IsDir() {
		return s
	}
	s.IsDir = true
	entries, err := os.ReadDir(name)
	if err != nil {
		return s
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			s.TFFiles++
		}
	}
	return s
}

// checkIgnore is `git check-ignore -q <dir>/`: 0 ignored, 1 not, anything
// else git refusing to say — with what it said, so the line can quote it.
func checkIgnore(dir string) (int, string) {
	cmd := exec.Command("git", "check-ignore", "-q", strings.TrimSuffix(dir, "/")+"/")
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = nil, &stderr
	said := func() string {
		s := strings.TrimSpace(stderr.String())
		if s == "" {
			s = "exited without a message"
		}
		return s
	}
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), said()
		}
		return -1, err.Error()
	}
	return 0, ""
}

// underRoot is whether dir, resolved against root, stays inside it.
func underRoot(root, dir string) bool {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	rel, err := filepath.Rel(root, filepath.Clean(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// promptsOnDisk is every prompts.* key the config FILE sets — not the
// defaults — and whether the path it names is a file under the root.
func promptsOnDisk(cfg *config.Config) []doctor.Prompt {
	raw, _ := cfg.User["prompts"].(map[string]any)
	var out []doctor.Prompt
	for _, key := range doctor.SortedKeys(raw) {
		path, _ := raw[key].(string)
		// An empty string or null is no override — `falconet prompt` prints
		// the embedded prompt for either, as the bash's `// ""` did — so
		// there is no file to look for and nothing to report.
		if path == "" {
			continue
		}
		p := doctor.Prompt{Key: key, Path: path}
		clean := filepath.Clean(path)
		p.Inside = path != "" && !filepath.IsAbs(path) && clean != ".." && !strings.HasPrefix(clean, "../")
		if p.Inside {
			info, err := os.Stat(clean)
			p.Exists = err == nil && info.Mode().IsRegular()
		}
		out = append(out, p)
	}
	return out
}
