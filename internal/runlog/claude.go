package runlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxEvent is the longest stream-json line Claude reads. A tool result can
// carry a whole file, so the bound is generous; a longer line is reported in
// one line and not shown.
const MaxEvent = 8 << 20

// The bounds on what one event shows. The log is for following the agent,
// not for reading everything it read: the change itself is on the branch.
const (
	// maxText is how much of one block of the agent's own text is shown.
	maxText = 2000
	// maxTextLines is how many lines of it.
	maxTextLines = 30
	// maxDetail is how much of a tool call's target, or of a tool error, is
	// shown, on its one line.
	maxDetail = 200
)

// Claude renders the Claude Code CLI's stream-json output, one event per
// line, as readable lines: what the agent said, each tool it called and the
// path or pattern it called it on, each tool call that failed, and how the
// session ended. Everything else is one short line or nothing. The event
// shapes are the Agent SDK's SDKMessage types.
//
// A line that is not JSON is shown as it is: a harness that prints a word of
// its own among the events is not silenced by the format it was given.
//
// Rendering decides nothing. What implement concludes is the harness's exit
// status, whatever the events say.
type Claude struct {
	// cwd is the session's working directory, from its init event, so a
	// tool's absolute path is shown relative to the repository root.
	cwd string
	// tools maps a tool call's id to its name, so a failed result can say
	// which tool failed.
	tools map[string]string
}

// event is the part of an SDKMessage this renderer reads.
type event struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// system/init
	Cwd            string `json:"cwd"`
	Model          string `json:"model"`
	PermissionMode string `json:"permissionMode"`
	Version        string `json:"claude_code_version"`

	// assistant, user: an object with content. A system event's message is
	// a string, so it is read only as the event's type says.
	Message json.RawMessage `json:"message"`

	// system/permission_denied
	ToolName string `json:"tool_name"`

	// system/hook_response
	HookName string `json:"hook_name"`
	Outcome  string `json:"outcome"`

	// result
	IsError    bool     `json:"is_error"`
	NumTurns   *int     `json:"num_turns"`
	DurationMS *float64 `json:"duration_ms"`
	CostUSD    *float64 `json:"total_cost_usd"`
	Errors     []string `json:"errors"`
	Denials    []denial `json:"permission_denials"`
}

// denial is one tool call the session's permission mode refused.
type denial struct {
	ToolName string `json:"tool_name"`
}

// block is one entry of a message's content.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// Render returns the lines one line of output shows. The lines may carry
// line breaks of their own, which the Sink splits and makes Neutral.
func (c *Claude) Render(line []byte) []string {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return []string{string(line)}
	}
	var ev event
	if json.Unmarshal(trimmed, &ev) != nil {
		// An object, and not in the shape of any event this renderer knows:
		// one line naming its type, never the object.
		var typed struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(trimmed, &typed)
		return []string{"event: " + detail(orText(typed.Type, "(no type)")) + ", in a shape this renderer cannot read"}
	}
	switch ev.Type {
	case "system":
		return c.system(&ev)
	case "assistant":
		return c.assistant(&ev)
	case "user":
		return c.user(&ev)
	case "result":
		return []string{result(&ev)}
	case "stream_event":
		// Partial messages, only printed when asked for; the whole message
		// follows as an assistant event.
		return nil
	case "":
		return []string{"event: a JSON line with no type"}
	default:
		return []string{"event: " + detail(ev.Type)}
	}
}

// progress is the system events that report progress on something the log
// shows another way, or not at all: an estimate of the thinking tokens so
// far, which arrives every few dozen of them, a hook starting or printing, a
// subagent's running totals, and the session turning busy or idle.
var progress = map[string]bool{
	"thinking_tokens":       true,
	"hook_started":          true,
	"hook_progress":         true,
	"task_progress":         true,
	"session_state_changed": true,
}

func (c *Claude) system(ev *event) []string {
	switch {
	case progress[ev.Subtype]:
		return nil
	case ev.Subtype == "permission_denied":
		// The refusal's message is also the failed tool result that follows,
		// which the tool error line shows.
		return []string{"tool refused: " + detail(orText(ev.ToolName, "(unnamed tool)"))}
	case ev.Subtype == "hook_response":
		if ev.Outcome == "success" {
			return nil
		}
		return []string{"hook: " + detail(orText(ev.HookName, "(unnamed hook)")) + " ended in " + detail(orText(ev.Outcome, "(no outcome)"))}
	case ev.Subtype != "init":
		return []string{"system: " + detail(orText(ev.Subtype, "(no subtype)"))}
	}
	c.cwd = ev.Cwd
	var parts []string
	if ev.Model != "" {
		parts = append(parts, "model "+detail(ev.Model))
	}
	if ev.Version != "" {
		parts = append(parts, "Claude Code "+detail(ev.Version))
	}
	if ev.PermissionMode != "" {
		parts = append(parts, "permission mode "+detail(ev.PermissionMode))
	}
	if len(parts) == 0 {
		return []string{"session: started"}
	}
	return []string{"session: " + strings.Join(parts, ", ")}
}

func (c *Claude) assistant(ev *event) []string {
	blocks, ok := content(ev)
	if !ok {
		return []string{"agent: a message this renderer cannot read"}
	}
	var out []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			out = append(out, prose("agent: ", b.Text))
		case "tool_use":
			if c.tools == nil {
				c.tools = map[string]string{}
			}
			name := orText(b.Name, "(unnamed tool)")
			c.tools[b.ID] = name
			line := "tool: " + detail(name)
			if target := c.target(b.Input); target != "" {
				line += " " + target
			}
			out = append(out, line)
		case "thinking", "redacted_thinking":
			// The model's private reasoning: long, and not what it did.
		default:
			out = append(out, "agent: a "+detail(orText(b.Type, "(untyped)"))+" block")
		}
	}
	return out
}

func (c *Claude) user(ev *event) []string {
	blocks, ok := content(ev)
	if !ok {
		// A plain string is the prompt, which is prompt.md.
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type != "tool_result" || !b.IsError {
			continue
		}
		name := c.tools[b.ToolUseID]
		line := "tool error:"
		if name != "" {
			line += " " + detail(name) + ":"
		}
		if text := resultText(b.Content); text != "" {
			line += " " + detail(text)
		}
		out = append(out, line)
	}
	return out
}

// result is the session's last line: how it ended, in how many turns, how
// long it took and what it cost, and the tool calls the permission mode
// refused. A session that stopped at the turn cap says so in words.
func result(ev *event) string {
	var how string
	switch ev.Subtype {
	case "success":
		how = "finished"
		if ev.IsError {
			how = "finished with an error"
		}
	case "error_max_turns":
		how = "stopped at the turn cap (--max-turns)"
	case "error_max_budget_usd":
		how = "stopped at the budget cap"
	case "error_during_execution":
		how = "stopped by an error during execution"
	default:
		how = "ended: " + detail(orText(ev.Subtype, "(no subtype)"))
	}
	var facts []string
	if ev.NumTurns != nil {
		facts = append(facts, plural(*ev.NumTurns, "turn"))
	}
	if ev.DurationMS != nil && *ev.DurationMS >= 0 {
		facts = append(facts, duration(*ev.DurationMS))
	}
	if ev.CostUSD != nil && *ev.CostUSD >= 0 {
		facts = append(facts, fmt.Sprintf("$%.4f", *ev.CostUSD))
	}
	if n := len(ev.Denials); n > 0 {
		names := map[string]bool{}
		for _, d := range ev.Denials {
			names[orText(d.ToolName, "(unnamed tool)")] = true
		}
		list := make([]string, 0, len(names))
		for name := range names {
			list = append(list, name)
		}
		sort.Strings(list)
		facts = append(facts, fmt.Sprintf("%s refused (%s)", plural(n, "tool call"), detail(strings.Join(list, ", "))))
	}
	line := "result: " + how
	if len(facts) > 0 {
		line += " — " + strings.Join(facts, ", ")
	}
	if len(ev.Errors) > 0 && strings.TrimSpace(ev.Errors[0]) != "" {
		line += "; " + detail(ev.Errors[0])
	}
	return line
}

// target is the one thing a tool call names: a path, a pattern, a command,
// an address, a query. A path under the session's directory is shown
// relative to it.
func (c *Claude) target(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	str := func(key string) string {
		s, _ := in[key].(string)
		return s
	}
	path := func(p string) string {
		if c.cwd != "" && filepath.IsAbs(p) {
			if rel, err := filepath.Rel(c.cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
		return p
	}
	for _, key := range []string{"file_path", "notebook_path"} {
		if p := str(key); p != "" {
			return detail(path(p))
		}
	}
	if p := str("pattern"); p != "" {
		where := str("path")
		if where == "" {
			where = str("glob")
		} else {
			where = path(where)
		}
		if where != "" {
			return detail(p) + " in " + detail(where)
		}
		return detail(p)
	}
	for _, key := range []string{"path", "command", "url", "query", "description"} {
		if s := str(key); s != "" {
			if key == "path" {
				s = path(s)
			}
			return detail(s)
		}
	}
	return ""
}

// content decodes a message's content as blocks. A message whose content
// is a string, or missing, is not blocks.
func content(ev *event) ([]block, bool) {
	var message struct {
		Content json.RawMessage `json:"content"`
	}
	if len(ev.Message) == 0 || json.Unmarshal(ev.Message, &message) != nil {
		return nil, false
	}
	var blocks []block
	if json.Unmarshal(message.Content, &blocks) != nil {
		return nil, false
	}
	return blocks, true
}

// resultText is a tool result's content, a string or a list of text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// prose is the agent's own text behind a label, its lines kept and
// indented under the label, cut at maxText bytes or maxTextLines lines with
// a marker saying how much was left out.
func prose(label, text string) string {
	text = strings.TrimSpace(text)
	cut := ""
	if len(text) > maxText {
		n := runeFloor(text, maxText)
		cut = fmt.Sprintf(" … [%d more bytes not shown]", len(text)-n)
		text = strings.TrimRightFunc(text[:n], unicode.IsSpace)
	}
	lines := split(text)
	if len(lines) > maxTextLines {
		cut = fmt.Sprintf(" … [%d more lines not shown]", len(lines)-maxTextLines)
		lines = lines[:maxTextLines]
	}
	indent := strings.Repeat(" ", utf8.RuneCountInString(label))
	for i := range lines {
		switch {
		case i == 0:
			lines[i] = label + lines[i]
		case strings.TrimSpace(lines[i]) != "":
			lines[i] = indent + lines[i]
		}
	}
	return strings.Join(lines, "\n") + cut
}

// detail is a value on one line: every line break and control character a
// space, runs of spaces one, cut at maxDetail bytes with a marker.
func detail(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > maxDetail {
		n := runeFloor(out, maxDetail)
		out = out[:n] + "…"
	}
	return out
}

// runeFloor is the largest index no greater than n that starts a rune.
func runeFloor(s string, n int) int {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// duration is milliseconds as a person reads them: 950ms, 42.1s, 3m05s.
func duration(ms float64) string {
	d := time.Duration(ms * float64(time.Millisecond))
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	}
}
