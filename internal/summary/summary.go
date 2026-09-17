// Package summary is the panel a run writes on its own page: one markdown
// block saying how the run ended, the issue, the branch, the agent passes
// used and the check's last word. The verb, cmd/falconet/summary.go, reads
// the workflow's contexts from the environment and appends what Render
// returns to $GITHUB_STEP_SUMMARY.
//
// # Where it is written
//
// Exactly one job writes it on every path through a gate job that ran: gate
// itself, when prepare did not say ready, and contain, which runs whenever
// prepare did. The panel says what those jobs can see: each job's result,
// the outputs the jobs declare, the writing job's own status, and contain's
// check and pause steps. It reads no prose. Which guard refused is
// failure-kind.txt's word, carried as an output of the implement job; why
// prepare did not say ready is prepare's own `reason` output.
//
// # What it never shows
//
// A step summary renders markdown and HTML, and most of what the panel
// reports came from somewhere else: a branch name made from an issue title,
// prepare's reason naming a label or a login, a job output the agent's job
// set. So every value that is not one of this file's fixed words is shown in
// a code span, on one line, with no backtick in it, which renders no link,
// image, mention, emphasis or HTML. The only links are to this repository's
// own issue, pull request and branch, built here from a validated server,
// repository, number or branch name. The issue's title and body, the check's
// output and any credential are not inputs at all.
package summary

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zetlen/falconet/internal/commit"
)

// Job is one entry of the workflow's `needs` context, as toJSON prints it.
type Job struct {
	Result  string            `json:"result"`
	Outputs map[string]string `json:"outputs"`
}

// Step is one entry of a job's `steps` context, as toJSON prints it.
type Step struct {
	Outcome string            `json:"outcome"`
	Outputs map[string]string `json:"outputs"`
}

// Run is everything the panel is written from.
type Run struct {
	// Job is where the panel is written: "gate" or "contain".
	Job string
	// Issue is the issue number, as the workflow input.
	Issue string
	// Server and Repository are $GITHUB_SERVER_URL and $GITHUB_REPOSITORY,
	// the only things links are built from.
	Server, Repository string
	// MaxAttempts is the workflow input.
	MaxAttempts string
	// JobStatus is job.status in the job the panel is written from:
	// "cancelled" when that job was cancelled or ran past its
	// timeout-minutes, which the job cannot tell apart.
	JobStatus string

	// Prepare is gate's Prepare step: toJSON(steps.prepare).
	Prepare Step

	// Needs is contain's toJSON(needs): gate, implement and publish.
	Needs map[string]Job
	// Check is the outcome of contain's step that looks for an ending on the
	// issue, and Pause the outcome of its pause step, which runs when that
	// step found none and also when it did not run or did not finish.
	Check, Pause string
}

// ParseJSON reads a toJSON value into v, and leaves v as it was when the
// text is empty or not JSON: a context that did not arrive is a run the
// panel knows less about, never a panel that is not written.
func ParseJSON(text string, v any) {
	if strings.TrimSpace(text) == "" {
		return
	}
	_ = json.Unmarshal([]byte(text), v)
}

// --- how the run ended -----------------------------------------------------

// Ending is one of the ways a run ends, as the panel names it.
type Ending string

// The endings. The first three are principle 4's: a pull request, a question,
// or a hand-off; Refused is a hand-off a guard decided. The rest are runs
// that stopped before any of them, or never started. Stopped is a job whose
// result is cancelled: a person cancelled the run, or the job ran past its
// timeout-minutes. The runner stops a job for both with the same message,
// so no job, and no status function, can say which it was.
const (
	Published     Ending = "published"
	NeedsInfo     Ending = "needs-info"
	ReadyForHuman Ending = "ready-for-human"
	Refused       Ending = "refused"
	Ineligible    Ending = "ineligible"
	InFlight      Ending = "in-flight"
	Failed        Ending = "failed"
	Stopped       Ending = "stopped"
	Unfinished    Ending = "unfinished"
)

// Decision is what Decide concluded, and the facts the panel shows for it.
type Decision struct {
	Ending Ending
	// Job is the job a failure or a stop happened in.
	Job string
	// Step is the step a failure happened in, when the job's outputs name it.
	Step string
	// Kind is commit's word, for Refused and a ReadyForHuman with nothing
	// to commit.
	Kind string
	// Cap is true for a ReadyForHuman whose check still failed at the cap.
	Cap bool
	// Reason is prepare's reason, for an ending gate decided.
	Reason string
}

// jobs in the order they run.
var jobs = []string{"gate", "implement", "publish"}

// Decide reads how the run ended.
func Decide(r Run) Decision {
	if r.Job == "gate" {
		return decideGate(r)
	}
	return decideContain(r)
}

// decideGate: prepare's word, else why there is none.
func decideGate(r Run) Decision {
	reason := r.Prepare.Outputs["reason"]
	switch r.Prepare.Outputs["outcome"] {
	case "ineligible":
		return Decision{Ending: Ineligible, Reason: reason}
	case "in-flight":
		return Decision{Ending: InFlight, Reason: reason}
	}
	switch r.Prepare.Outcome {
	case "failure":
		return Decision{Ending: Failed, Job: "gate", Step: "prepare", Reason: reason}
	case "cancelled":
		return Decision{Ending: Stopped, Job: "gate"}
	case "success":
		// A prepare that succeeded said a word; a word this panel does not
		// know is not a run it can describe.
		return Decision{Ending: Unfinished, Job: "gate", Reason: reason}
	}
	// Prepare did not run: a step before it failed, or the job was stopped
	// before it started.
	if r.JobStatus == "cancelled" {
		return Decision{Ending: Stopped, Job: "gate"}
	}
	return Decision{Ending: Failed, Job: "gate", Step: "before-prepare"}
}

// decideContain: an ending publish reached wins, because what is on the
// issue is what a person sees; then the first job, in order, that stopped or
// failed; then contain itself stopping.
func decideContain(r Run) Decision {
	implement, publish := r.Needs["implement"], r.Needs["publish"]
	outcome, check, kind := implement.Outputs["outcome"], implement.Outputs["check"], implement.Outputs["kind"]
	if implement.Result == "success" && publish.Result == "success" {
		switch {
		case outcome == "success" && check != "fail":
			return Decision{Ending: Published}
		case outcome == "success" && check == "fail":
			return Decision{Ending: ReadyForHuman, Cap: true}
		case outcome == "needs-info":
			return Decision{Ending: NeedsInfo}
		case outcome == "failure" && commit.Kind(kind).Guard():
			return Decision{Ending: Refused, Kind: kind}
		case outcome == "failure":
			return Decision{Ending: ReadyForHuman, Kind: kind}
		}
	}
	for _, name := range jobs {
		switch r.Needs[name].Result {
		case "cancelled":
			return Decision{Ending: Stopped, Job: name}
		case "failure":
			return Decision{Ending: Failed, Job: name, Step: r.Needs[name].Outputs["failed"]}
		}
	}
	if r.JobStatus == "cancelled" {
		return Decision{Ending: Stopped, Job: "contain"}
	}
	return Decision{Ending: Unfinished}
}

// --- the panel --------------------------------------------------------------

// guards names each guard as the README does.
var guards = map[commit.Kind]string{
	commit.KindGitMachinery: "the checkout's own git machinery",
	commit.KindRename:       "the rename refusal",
	commit.KindConfigFile:   "the config file itself",
	commit.KindPaths:        "the path allowlist",
	commit.KindContent:      "the content denylist",
	commit.KindSecret:       "the secret scan",
}

// nothing says why a run had nothing to commit.
var nothing = map[commit.Kind]string{
	commit.KindUnchanged:   "The agent left the tree unchanged and asked no question.",
	commit.KindNoMessage:   "The agent changed files and wrote no commit message.",
	commit.KindEmptyChange: "The agent's change staged to nothing.",
}

// steps names the steps a job's `failed` output can name.
var steps = map[string]string{
	"before-prepare": "a step before Prepare (the App token, the checkout or the install)",
	"prepare":        "Prepare",
	"loop":           "Implement, and check (the harness or the check could not run)",
	"commit":         "Commit",
	"push":           "Push",
	"pr":             "Open the pull request",
}

// Render is the panel, in markdown, ending with a newline.
func Render(r Run) string {
	d := Decide(r)
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	implement := r.Needs["implement"]
	switch d.Ending {
	case Published:
		line("### falconet: pull request opened")
		line("")
		if link := r.pullLink(r.Needs["publish"].Outputs["pr"]); link != "" {
			line("%s is open for a person to review.", link)
		} else {
			line("A pull request is open for a person to review. Its address did not validate, so it is not linked here.")
		}
	case NeedsInfo:
		line("### falconet: asked the requester a question")
		line("")
		line("The agent needs more from the requester. The issue is labelled `needs-info`, and a comment from someone with write access runs it again.")
	case ReadyForHuman:
		if d.Cap {
			line("### falconet: handed to a person, the check still fails")
			line("")
			line("The change is committed and pushed, and the repository's own check failed on the last of the agent passes allowed. The issue is labelled `ready-for-human`.")
		} else {
			line("### falconet: handed to a person, nothing to commit")
			line("")
			if text, ok := nothing[commit.Kind(d.Kind)]; ok {
				line("%s The issue is labelled `ready-for-human`.", text)
			} else {
				line("The commit step prepared no change, for a reason it named as %s. The issue is labelled `ready-for-human`.", code(d.Kind))
			}
		}
	case Refused:
		line("### falconet: refused by a guard, %s", guards[commit.Kind(d.Kind)])
		line("")
		line("Nothing was committed. A guard refusal is terminal: nothing is sent back to the agent. The issue is labelled `ready-for-human`.")
	case Ineligible:
		line("### falconet: not started, ineligible")
		line("")
		line("Nothing was posted and nothing changed on the issue.")
	case InFlight:
		line("### falconet: not started, already in flight")
		line("")
		line("An open pull request already carries this issue. Nothing was posted and nothing changed.")
	case Failed:
		line("### falconet: failed in %s", jobName(d.Job))
		line("")
		if name, ok := steps[d.Step]; ok {
			line("The step that failed: %s. The job's log marks it.", name)
		} else if d.Step != "" {
			line("The step that failed is named %s. The job's log marks it.", code(d.Step))
		} else {
			line("The job's log marks the step that failed.")
		}
		if r.Job == "gate" {
			line("")
			line("A gate that fails posts nothing: the requester has not been told.")
		}
	case Stopped:
		line("### falconet: stopped in %s, cancelled or out of time", jobName(d.Job))
		line("")
		line("The job was cancelled, or stopped at its `timeout-minutes`, before it finished. Its log says which.")
	default:
		line("### falconet: no ending")
		line("")
		line("Every job that ran finished, and none of them reached an ending this panel knows.")
	}

	if r.Job == "contain" && (r.Pause == "success" || r.Pause == "failure") {
		line("")
		switch {
		case r.Check == "success" && r.Pause == "success":
			line("**contain** found no ending on the issue, and paused it `ready-for-human` with a link to this run.")
		case r.Check == "success":
			line("**contain** found no ending on the issue and could not pause it: nobody has been told.")
		case r.Pause == "success":
			line("**contain** could not check the issue for an ending, and paused it `ready-for-human` with a link to this run.")
		default:
			line("**contain** could not check the issue for an ending, and could not pause it.")
		}
	}

	line("")
	line("- **Issue:** %s", r.issueLink())
	if d.Reason != "" {
		line("- **Prepare's reason:** %s", code(d.Reason))
	}
	if r.Job == "contain" {
		if branch := r.Needs["gate"].Outputs["branch"]; branch != "" {
			pushed := implement.Outputs["outcome"] == "success" && r.Needs["publish"].Result == "success"
			if link := r.branchLink(branch); pushed && link != "" {
				line("- **Branch:** %s", link)
			} else {
				line("- **Branch:** %s", code(branch))
			}
		}
		if implement.Result != "" && implement.Result != "skipped" {
			passes := implement.Outputs["passes"]
			if digits.MatchString(passes) && digits.MatchString(r.MaxAttempts) {
				line("- **Agent passes:** %s of %s", passes, r.MaxAttempts)
			} else if passes != "" {
				line("- **Agent passes:** %s", code(passes))
			}
			switch check := implement.Outputs["check"]; check {
			case "pass", "fail", "skipped":
				line("- **Check's last word:** `%s`", check)
			case "":
				line("- **Check's last word:** none, the loop did not finish")
			default:
				line("- **Check's last word:** %s", code(check))
			}
		}
	}
	return b.String()
}

func jobName(job string) string {
	if job == "contain" || slices.Contains(jobs, job) {
		return "**" + job + "**"
	}
	return code(job)
}

// --- values and links ---------------------------------------------------------

// maxValue is the most of one value the panel shows.
const maxValue = 300

// code is a value from outside, shown as a code span: every line break and
// control character a space, spaces collapsed, every backtick an apostrophe,
// cut at maxValue runes. A code span renders its content as text, and with no
// backtick and no line break in it nothing can end it early.
func code(s string) string {
	var b strings.Builder
	space := false
	n := 0
	cut := false
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsSpace(r) || unicode.IsControl(r) ||
			unicode.In(r, unicode.Zl, unicode.Zp, unicode.Cf) {
			space = true
			continue
		}
		if n >= maxValue {
			cut = true
			break
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		if r == '`' {
			r = '\''
		}
		b.WriteRune(r)
		n++
	}
	out := b.String()
	if out == "" {
		return "(empty)"
	}
	if cut {
		out += "…"
	}
	return "`" + out + "`"
}

var (
	digits = regexp.MustCompile(`^[0-9]{1,10}$`)
	server = regexp.MustCompile(`^https://[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*(:[0-9]{1,5})?$`)
	repo   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
	branch = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]{0,250}$`)
)

// base is the repository's address, or empty when either half does not
// validate, in which case nothing is linked.
func (r Run) base() string {
	if !server.MatchString(r.Server) || !repo.MatchString(r.Repository) ||
		strings.Contains(r.Repository, "..") {
		return ""
	}
	return r.Server + "/" + r.Repository
}

func (r Run) issueLink() string {
	if !digits.MatchString(r.Issue) {
		return code(r.Issue)
	}
	if base := r.base(); base != "" {
		return fmt.Sprintf("[#%s](%s/issues/%s)", r.Issue, base, r.Issue)
	}
	return "#" + r.Issue
}

// pullLink is a link to the pull request when url is exactly this
// repository's pull request address, and empty otherwise.
func (r Run) pullLink(url string) string {
	base := r.base()
	if base == "" {
		return ""
	}
	number, ok := strings.CutPrefix(url, base+"/pull/")
	if !ok || !digits.MatchString(number) {
		return ""
	}
	return fmt.Sprintf("[#%s](%s/pull/%s)", number, base, number)
}

// branchLink is a link to the branch when its name is plain enough to be a
// path in a URL as it is, and empty otherwise.
func (r Run) branchLink(name string) string {
	base := r.base()
	if base == "" || !branch.MatchString(name) || strings.Contains(name, "..") || strings.Contains(name, "//") {
		return ""
	}
	return fmt.Sprintf("[%s](%s/tree/%s)", code(name), base, name)
}

// Fallback is the panel written when the summary cannot be rendered at all.
// It holds no value from anywhere.
const Fallback = "### falconet: no summary\n\nThis job could not describe the run. Its log says how the run ended.\n"
