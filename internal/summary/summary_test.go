package summary

import (
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/quick"

	"github.com/zetlen/falconet/internal/commit"
)

const (
	testServer = "https://github.com"
	testRepo   = "acme/infra"
)

func contain(needs map[string]Job) Run {
	return Run{Job: "contain", Issue: "42", Server: testServer, Repository: testRepo, MaxAttempts: "3", Needs: needs}
}

func ready(implement, publish Job) map[string]Job {
	return map[string]Job{
		"gate":      {Result: "success", Outputs: map[string]string{"outcome": "ready", "branch": "issue-42-add-a-line"}},
		"implement": implement,
		"publish":   publish,
	}
}

func impl(result, outcome, check, passes string, extra ...string) Job {
	out := map[string]string{"outcome": outcome, "check": check, "passes": passes}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return Job{Result: result, Outputs: out}
}

func pub(result string, extra ...string) Job {
	out := map[string]string{}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return Job{Result: result, Outputs: out}
}

var skipped = Job{Result: "skipped", Outputs: map[string]string{}}

// Every terminal state a run reaches, from what the two jobs that write the
// panel can see.
func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		run  Run
		want Decision
	}{
		{"gate: ineligible, with prepare's reason",
			Run{Job: "gate", Prepare: Step{Outcome: "success", Outputs: map[string]string{"outcome": "ineligible", "reason": "issue #42 is not labelled 'falconet'"}}},
			Decision{Ending: Ineligible, Reason: "issue #42 is not labelled 'falconet'"}},
		{"gate: in flight",
			Run{Job: "gate", Prepare: Step{Outcome: "success", Outputs: map[string]string{"outcome": "in-flight", "reason": "issue #42 already has an open PR: #57 — nothing to do"}}},
			Decision{Ending: InFlight, Reason: "issue #42 already has an open PR: #57 — nothing to do"}},
		{"gate: prepare refused mechanically",
			Run{Job: "gate", Prepare: Step{Outcome: "failure", Outputs: map[string]string{"reason": "prepare: working tree is dirty before the agent ran"}}},
			Decision{Ending: Failed, Job: "gate", Step: "prepare", Reason: "prepare: working tree is dirty before the agent ran"}},
		{"gate: a step before prepare failed",
			Run{Job: "gate", Prepare: Step{Outcome: "skipped"}},
			Decision{Ending: Failed, Job: "gate", Step: "before-prepare"}},
		{"gate: no steps context at all is still a failure before prepare",
			Run{Job: "gate"},
			Decision{Ending: Failed, Job: "gate", Step: "before-prepare"}},
		{"gate: prepare cancelled or out of time",
			Run{Job: "gate", JobStatus: "cancelled", Prepare: Step{Outcome: "cancelled"}},
			Decision{Ending: Stopped, Job: "gate"}},
		{"gate: cancelled or out of time before prepare started",
			Run{Job: "gate", JobStatus: "cancelled", Prepare: Step{Outcome: "skipped"}},
			Decision{Ending: Stopped, Job: "gate"}},

		{"published",
			contain(ready(impl("success", "success", "pass", "1"), pub("success", "pr", "https://github.com/acme/infra/pull/57"))),
			Decision{Ending: Published}},
		{"published with no check configured",
			contain(ready(impl("success", "success", "skipped", "1"), pub("success"))),
			Decision{Ending: Published}},
		{"needs-info",
			contain(ready(impl("success", "needs-info", "skipped", "1"), pub("success"))),
			Decision{Ending: NeedsInfo}},
		{"ready-for-human: the check failed at the cap",
			contain(ready(impl("success", "success", "fail", "3"), pub("success"))),
			Decision{Ending: ReadyForHuman, Cap: true}},
		{"ready-for-human: nothing to commit",
			contain(ready(impl("success", "failure", "pass", "1", "kind", "no-message"), pub("success"))),
			Decision{Ending: ReadyForHuman, Kind: "no-message"}},
		{"refused by the path allowlist",
			contain(ready(impl("success", "failure", "pass", "1", "kind", "paths"), pub("success"))),
			Decision{Ending: Refused, Kind: "paths"}},
		{"a kind that is not a guard is never a refusal",
			contain(ready(impl("success", "failure", "pass", "1", "kind", "paths\n"), pub("success"))),
			Decision{Ending: ReadyForHuman, Kind: "paths\n"}},
		{"an ending publish reached beats contain stopping after it",
			func() Run {
				r := contain(ready(impl("success", "success", "pass", "1"), pub("success", "pr", "https://github.com/acme/infra/pull/57")))
				r.JobStatus = "cancelled"
				return r
			}(),
			Decision{Ending: Published}},
		{"the hand-over failed after publish reached it: failed in publish",
			contain(ready(impl("success", "needs-info", "skipped", "1"), pub("failure"))),
			Decision{Ending: Failed, Job: "publish"}},
		{"the pull request could not be opened",
			contain(ready(impl("success", "success", "pass", "1"), pub("failure", "failed", "pr"))),
			Decision{Ending: Failed, Job: "publish", Step: "pr"}},
		{"the harness failed in the loop",
			contain(ready(impl("failure", "", "", "1", "failed", "loop"), pub("failure", "failed", "push"))),
			Decision{Ending: Failed, Job: "implement", Step: "loop"}},
		{"implement cancelled or out of time, whatever contain's own status",
			func() Run {
				r := contain(ready(impl("cancelled", "", "", "2"), skipped))
				r.JobStatus = "success"
				return r
			}(),
			Decision{Ending: Stopped, Job: "implement"}},
		{"implement failed, and publish after it stopped: the first job named",
			contain(ready(impl("failure", "", "", "1", "failed", "loop"), pub("cancelled"))),
			Decision{Ending: Failed, Job: "implement", Step: "loop"}},
		{"contain itself cancelled or out of time, and every job before it green",
			func() Run {
				r := contain(ready(impl("success", "", "pass", "1"), pub("success")))
				r.JobStatus = "cancelled"
				return r
			}(),
			Decision{Ending: Stopped, Job: "contain"}},
		{"gate said ready and failed handing the checkout over",
			contain(map[string]Job{"gate": {Result: "failure", Outputs: map[string]string{"outcome": "ready"}}, "implement": skipped, "publish": skipped}),
			Decision{Ending: Failed, Job: "gate"}},
		{"every job green and no ending",
			contain(ready(impl("success", "", "pass", "1"), pub("success"))),
			Decision{Ending: Unfinished}},
		{"nothing arrived at all",
			Run{Job: "contain"},
			Decision{Ending: Unfinished}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.run); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Decide = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestRenderPanels(t *testing.T) {
	cases := []struct {
		name string
		run  Run
		want []string
		not  []string
	}{
		{"published links this repository's pull request, issue and branch",
			contain(ready(impl("success", "success", "pass", "1"), pub("success", "pr", "https://github.com/acme/infra/pull/57"))),
			[]string{
				"### falconet: pull request opened\n",
				"[#57](https://github.com/acme/infra/pull/57) is open for a person to review.",
				"- **Issue:** [#42](https://github.com/acme/infra/issues/42)",
				"- **Branch:** [`issue-42-add-a-line`](https://github.com/acme/infra/tree/issue-42-add-a-line)",
				"- **Agent passes:** 1 of 3",
				"- **Check's last word:** `pass`",
			}, nil},
		{"a pull request address from anywhere else is not linked",
			contain(ready(impl("success", "success", "pass", "1"), pub("success", "pr", "https://evil.example/acme/infra/pull/57"))),
			[]string{"Its address did not validate, so it is not linked here."},
			[]string{"evil.example", "/pull/"}},
		{"refused names the guard",
			contain(ready(impl("success", "failure", "pass", "2", "kind", "secret"), pub("success"))),
			[]string{"### falconet: refused by a guard, the secret scan\n", "- **Branch:** `issue-42-add-a-line`", "- **Agent passes:** 2 of 3"},
			[]string{"](https://github.com/acme/infra/tree/"}},
		{"the cap says the check still fails",
			contain(ready(impl("success", "success", "fail", "3"), pub("success"))),
			[]string{"### falconet: handed to a person, the check still fails\n", "- **Check's last word:** `fail`", "- **Agent passes:** 3 of 3"}, nil},
		{"nothing to commit says why",
			contain(ready(impl("success", "failure", "skipped", "1", "kind", "unchanged"), pub("success"))),
			[]string{"### falconet: handed to a person, nothing to commit\n", "The agent left the tree unchanged and asked no question."}, nil},
		{"a failure names the job and step, and what contain did",
			func() Run {
				r := contain(ready(impl("failure", "", "", "1", "failed", "loop"), pub("failure")))
				r.Check, r.Pause = "success", "success"
				return r
			}(),
			[]string{"### falconet: failed in **implement**\n", "Implement, and check (the harness or the check could not run)",
				"**contain** found no ending on the issue, and paused it `ready-for-human`", "- **Check's last word:** none, the loop did not finish"},
			[]string{"requester has not been told"}},
		{"contain that could not pause says so",
			func() Run {
				r := contain(ready(impl("cancelled", "", "", "1"), pub("failure")))
				r.Check, r.Pause = "success", "failure"
				return r
			}(),
			[]string{"### falconet: stopped in **implement**, cancelled or out of time\n",
				"The job was cancelled, or stopped at its `timeout-minutes`, before it finished.",
				"could not pause it: nobody has been told"}, nil},
		{"contain whose check did not run says that, and not that it found no ending",
			func() Run {
				r := contain(ready(impl("success", "success", "pass", "1"), pub("success", "pr", "https://github.com/acme/infra/pull/57")))
				r.Check, r.Pause = "skipped", "failure"
				return r
			}(),
			[]string{"### falconet: pull request opened\n", "**contain** could not check the issue for an ending, and could not pause it."},
			[]string{"found no ending", "nobody has been told"}},
		{"contain whose check failed and that paused says so",
			func() Run {
				r := contain(ready(impl("failure", "", "", "1", "failed", "loop"), pub("failure")))
				r.Check, r.Pause = "failure", "success"
				return r
			}(),
			[]string{"**contain** could not check the issue for an ending, and paused it `ready-for-human` with a link to this run."},
			[]string{"found no ending"}},
		{"ineligible shows prepare's reason in a code span",
			Run{Job: "gate", Issue: "42", Server: testServer, Repository: testRepo,
				Prepare: Step{Outcome: "success", Outputs: map[string]string{"outcome": "ineligible", "reason": "issue #42: mallory does not hold write on the repository"}}},
			[]string{"### falconet: not started, ineligible\n", "- **Prepare's reason:** `issue #42: mallory does not hold write on the repository`"},
			[]string{"Branch", "Agent passes"}},
		{"a failed gate says the requester has not been told",
			Run{Job: "gate", Issue: "42", Prepare: Step{Outcome: "failure", Outputs: map[string]string{"reason": "prepare: could not read issue #42: HTTP 404"}}},
			[]string{"### falconet: failed in **gate**\n", "The step that failed: Prepare.", "requester has not been told", "- **Issue:** #42"}, nil},
		{"with no valid server nothing is linked",
			func() Run {
				r := contain(ready(impl("success", "success", "pass", "1"), pub("success", "pr", "javascript:alert(1)//github.com/acme/infra/pull/57")))
				r.Server = "javascript:alert(1)//"
				return r
			}(),
			[]string{"- **Issue:** #42", "- **Branch:** `issue-42-add-a-line`"},
			[]string{"](", "javascript"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Render(c.run)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("panel lacks %q:\n%s", w, got)
				}
			}
			for _, n := range c.not {
				if strings.Contains(got, n) {
					t.Errorf("panel contains %q:\n%s", n, got)
				}
			}
			if strings.Count(got, "### ") != 1 {
				t.Errorf("panel has %d headings:\n%s", strings.Count(got, "### "), got)
			}
		})
	}
}

// Every guard has a name, and every other way to have nothing to commit
// has a sentence, so no kind commit can write reaches the panel as a bare
// word.
func TestEveryKindIsNamed(t *testing.T) {
	for _, k := range commit.Kinds {
		_, guard := guards[k]
		_, other := nothing[k]
		if guard != k.Guard() || other == k.Guard() {
			t.Errorf("%s: named as a guard %v, as nothing to commit %v; Guard() = %v", k, guard, other, k.Guard())
		}
	}
}

// --- nothing from outside renders --------------------------------------------

const marker = "ZQXJ"

type hostile string

func (hostile) Generate(r *rand.Rand, _ int) reflect.Value {
	pieces := []string{marker, "`", "``", "\n", "\r", "\u2028", "[a](https://evil.example)", "![i](x)", "<img src=x>",
		"<script>", "*", "_", "#", "|", "@octocat", "https://evil.example", "www.evil.example", "&lt;", "\\", ")", "](", " ",
		"\u202e", "\x00", "-", "> ", "::", "issue-42", "/", "..", "pass", "success", "loop", "paths", "0"}
	var b strings.Builder
	for i, n := 0, 1+r.Intn(12); i < n; i++ {
		b.WriteString(pieces[r.Intn(len(pieces))])
	}
	if r.Intn(3) == 0 {
		b.WriteString(marker)
	}
	return reflect.ValueOf(hostile(b.String()))
}

// codeSpans removes every code span; what is left is what renders as
// markdown.
var codeSpans = regexp.MustCompile("`[^`\n]*`")

// Whatever arrives in any input, the panel's markdown outside its code spans
// carries none of it: no marker from a value, no HTML, no image, no link but
// this repository's own, and every code span closes on its line.
func TestNothingFromOutsideRenders(t *testing.T) {
	property := func(issue, reason, branch, kind, failed, check, passes, pr, word, outcome, result hostile, gateJob bool, validBase bool) bool {
		r := Run{Job: "contain", Issue: string(issue), MaxAttempts: string(passes)}
		if validBase {
			r.Server, r.Repository = testServer, testRepo
		} else {
			r.Server, r.Repository = string(pr), string(branch)
		}
		results := []string{"success", "failure", "cancelled", "skipped", string(result)}
		pick := func(i int) string { return results[i%len(results)] }
		r.Needs = map[string]Job{
			"gate":      {Result: pick(len(reason)), Outputs: map[string]string{"outcome": "ready", "branch": string(branch), "failed": string(failed)}},
			"implement": {Result: pick(len(kind)), Outputs: map[string]string{"outcome": []string{"success", "failure", "needs-info", string(outcome)}[len(word)%4], "check": string(check), "passes": string(passes), "kind": string(kind), "failed": string(failed)}},
			"publish":   {Result: pick(len(pr)), Outputs: map[string]string{"pr": string(pr), "failed": string(failed)}},
		}
		r.Pause, r.Check, r.JobStatus = pick(len(check)), pick(len(passes)), pick(len(issue))
		r.Prepare = Step{Outcome: pick(len(outcome)), Outputs: map[string]string{"outcome": []string{"ineligible", "in-flight", string(word)}[len(issue)%3], "reason": string(reason)}}
		if gateJob {
			r.Job = "gate"
		}
		out := Render(r)
		for i, line := range strings.Split(out, "\n") {
			if strings.Count(line, "`")%2 != 0 {
				t.Logf("line %d has an unclosed code span: %q", i, line)
				return false
			}
		}
		outside := codeSpans.ReplaceAllString(out, "")
		base := regexp.QuoteMeta(testServer + "/" + testRepo)
		outside = regexp.MustCompile(`\]\(`+base+`/(pull|issues)/[0-9]+\)`).ReplaceAllString(outside, "]")
		outside = regexp.MustCompile(`\]\(`+base+`/tree/[A-Za-z0-9._/-]+\)`).ReplaceAllString(outside, "]")
		for _, bad := range []string{marker, "<", "](", "![", "http", "www.", "@", "\u202e", "\x00", "\r", "\u2028"} {
			if strings.Contains(outside, bad) {
				t.Logf("%q outside a code span or a validated link:\n%s", bad, out)
				return false
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}

// A context that did not arrive, or is not JSON, leaves the run as it was:
// a panel that knows less, never one that is not written.
func TestParseJSONLeavesTheRunOnWhatIsNotJSON(t *testing.T) {
	for _, text := range []string{"", "  \n", `{"gate": not json`, "null-ish", `{"gate":{"result":`} {
		r := Run{Needs: map[string]Job{"gate": {Result: "success"}}}
		ParseJSON(text, &r.Needs)
		if !reflect.DeepEqual(r.Needs, map[string]Job{"gate": {Result: "success"}}) {
			t.Errorf("ParseJSON(%q) changed the run: %+v", text, r.Needs)
		}
	}
	var needs map[string]Job
	ParseJSON(`{"implement":{"result":"failure","outputs":{"failed":"loop"}}}`, &needs)
	if needs["implement"].Outputs["failed"] != "loop" {
		t.Errorf("ParseJSON did not read a context: %+v", needs)
	}
}

func TestCode(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "`plain`"},
		{"a `tick`", "`a 'tick'`"},
		{"two\nlines\r\nhere", "`two lines here`"},
		{"  padded  ", "`padded`"},
		{"", "(empty)"},
		{"\n\t", "(empty)"},
		{strings.Repeat("x", maxValue+5), "`" + strings.Repeat("x", maxValue) + "…`"},
	} {
		if got := code(c.in); got != c.want {
			t.Errorf("code(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
