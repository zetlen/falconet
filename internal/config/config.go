// Package config is one JSON file, every key optional, with defaults that
// reproduce the origin repository's behavior exactly.
//
// The defaults live here as a JSON document rather than as a fallback at each
// call site, and the user's file is merged OVER them the way jq's `*` merges:
// objects recurse, and everything else — arrays included — is replaced
// wholesale. That is deliberate. Setting paths.allow means "this list instead
// of the default", never "these in addition to *.tf" — an allowlist that
// grows by accident is not an allowlist. A default spelled at a call site
// drifts from the schema in docs/adr/0003-the-cli-surface.md the first time
// someone edits one and not the other.
//
// Discovery, in precedence order:
//
//  1. --config PATH        (each verb parses its own flag and passes it here)
//  2. $FALCONET_CONFIG
//  3. ./.github/falconet.json
//  4. nothing — the defaults stand alone
//
// Resolution 3 is relative to the working directory, not to the binary: verbs
// cd to the repository root before they read config, and a tool run inside a
// repository should read that repository's configuration rather than one
// beside its own install.
//
// A malformed config is an error, never a silent fall back to defaults: "your
// config is being ignored" is exactly the failure that gets discovered in
// production, on the run where the allowlist mattered.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Defaults is the schema with every default, as the JSON document lib/config.sh
// carried. Every key in docs/adr/0003-the-cli-surface.md is here but one, so
// a verb never has to ask whether a key is set. The one is prompts: its
// default was issue #3 — a path relative to the consumer's repository, which
// made the default an override and the shipped prompt unreachable — and the
// shipped prompts are embedded in the binary now, so an absent key means
// exactly that. See Schema.Prompts.
const Defaults = `{
  "handoff_dir": ".falconet",
  "issue": {
    "queue_label": "infra-request",
    "opt_out_text": "Not eligible for AI agents",
    "branch_prefix": "issue-",
    "in_flight_prefixes": ["issue-", "claude/issue-"],
    "blocking_labels": ["needs-info", "ready-for-human", "do-not-apply", "wontfix"]
  },
  "labels": {
    "needs_info": "needs-info",
    "human": "ready-for-human",
    "pr": "needs-plan-review"
  },
  "paths": {
    "allow": ["*.tf"],
    "deny_content": [
      "data \"external\"",
      "provisioner",
      "local-exec",
      "remote-exec",
      "templatefile(",
      "filebase64(",
      "file("
    ]
  },
  "stacks": {
    "plan": ["dns"],
    "validate_only": ["workspace", "site"]
  },
  "plan": {
    "command": "tofu -chdir={stack} plan -no-color -input=false -refresh=false -lock=false"
  }
}`

// Schema is the typed view of a resolved config — what a verb reads. Field
// tags are the keys of the JSON file; the document they decode from is the
// defaults with the user's file merged over, so every field is populated.
type Schema struct {
	HandoffDir string `json:"handoff_dir"`
	Issue      struct {
		QueueLabel       string   `json:"queue_label"`
		OptOutText       string   `json:"opt_out_text"`
		BranchPrefix     string   `json:"branch_prefix"`
		InFlightPrefixes []string `json:"in_flight_prefixes"`
		BlockingLabels   []string `json:"blocking_labels"`
	} `json:"issue"`
	Labels struct {
		NeedsInfo string `json:"needs_info"`
		Human     string `json:"human"`
		PR        string `json:"pr"`
	} `json:"labels"`
	Paths struct {
		Allow []string `json:"allow"`
		// DenyContent is tested IN ORDER: `templatefile(` before `file(`, or a
		// templatefile() call is reported as file() — the right refusal naming
		// the wrong construct. Nothing downstream can recover the distinction,
		// so it has to survive here.
		DenyContent []string `json:"deny_content"`
	} `json:"paths"`
	Stacks struct {
		Plan         []string `json:"plan"`
		ValidateOnly []string `json:"validate_only"`
	} `json:"stacks"`
	Plan struct {
		Command string `json:"command"`
	} `json:"plan"`
	// Prompts is keyed by prompt name with `-` folded to `_`, and is a map
	// because `falconet prompt <name>` looks names up dynamically. It has no
	// default (#3): an absent key means the prompt embedded in the binary,
	// and a set one is a path relative to the repository root — an override,
	// and nothing else. A value of any other type is refused with the rest
	// of the schema.
	Prompts map[string]string `json:"prompts"`
}

// Config is a resolved configuration: the path that was read, the merged
// document, and its typed view.
type Config struct {
	// File is the path actually read, as it was resolved — ".github/falconet.json",
	// not an absolute path — or empty when no file was found and the defaults
	// stand alone. Some verbs report it, and every test wants to know which of
	// the four resolutions fired.
	File string
	// Doc is the defaults with the file merged over them. Numbers are
	// json.Number, so they print as they were written.
	Doc map[string]any
	// User is the file's own document, before the merge — what the operator
	// actually set — or nil when no file was found. doctor reads it to tell
	// an override from a default: a prompt path the file names must exist
	// under the repository root, where a default names the shipped prompt.
	User map[string]any
	// Schema is Doc, typed.
	Schema Schema
}

// Load resolves and reads the configuration. explicit is the verb's --config
// argument, or empty.
func Load(explicit string) (*Config, error) {
	path := ""
	switch {
	case explicit != "":
		if !isFile(explicit) {
			return nil, fmt.Errorf("--config names no file: %s", explicit)
		}
		path = explicit
	case os.Getenv("FALCONET_CONFIG") != "":
		p := os.Getenv("FALCONET_CONFIG")
		if !isFile(p) {
			return nil, fmt.Errorf("$FALCONET_CONFIG names no file: %s", p)
		}
		path = p
	case isFile(".github/falconet.json"):
		path = ".github/falconet.json"
	}

	doc, err := parseObject([]byte(Defaults))
	if err != nil {
		// The defaults are a constant in this file; this is a build defect,
		// not a runtime condition, and the unit tests make it unreachable.
		return nil, fmt.Errorf("the built-in defaults are not valid JSON: %v", err)
	}

	var user map[string]any
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s cannot be read: %v", path, err)
		}
		user, err = parseObject(raw)
		if err != nil {
			// The message names OUR file rather than leaving a bare parse
			// error in a log with nothing around it.
			return nil, fmt.Errorf("%s is not valid JSON: %v", path, err)
		}
		doc = Merge(doc, user)
	}

	cfg := &Config{File: path, Doc: doc, User: user}
	if err := cfg.decodeSchema(); err != nil {
		return nil, fmt.Errorf("%s does not match the schema: %v", orDefault(path, "the built-in defaults"), err)
	}
	return cfg, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// parseObject reads exactly one JSON object. Numbers stay json.Number.
// Anything after the object is refused: jq would have slurped a second value
// and silently used only the first, and a file with two documents in it is a
// mistake worth hearing about.
func parseObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value in the file")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("the top-level value is not an object")
	}
	return obj, nil
}

// Merge returns base with over merged on top, the way jq's `*` does it: where
// both sides hold an object the merge recurses; anywhere else — arrays,
// strings, numbers, booleans, null — the value from over replaces the value
// from base. Neither input is modified.
func Merge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bo, ok := out[k].(map[string]any); ok {
			if vo, ok := v.(map[string]any); ok {
				out[k] = Merge(bo, vo)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (c *Config) decodeSchema() error {
	raw, err := json.Marshal(c.Doc)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, &c.Schema)
}

// Get returns the value at a jq-style path such as `.issue.queue_label`. A
// path that names nothing yields nil, which Raw prints as `null` — the
// answer jq gives. Only the dotted-identifier form, with optional quoted
// segments (`.prompts."pause-needs-info"`), is understood; the tests and the
// verbs need nothing more, and a filter language is not this package's job.
func (c *Config) Get(path string) (any, error) {
	segments, err := splitPath(path)
	if err != nil {
		return nil, err
	}
	var cur any = c.Doc
	for _, seg := range segments {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, nil
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, nil
		}
	}
	return cur, nil
}

// Array returns the elements at path, IN ORDER. A missing or null value is an
// empty list — jq's `// []` — and a value that is not an array is an error,
// because a verb iterating a string one character at a time is a bug that
// should not be quiet.
func (c *Config) Array(path string) ([]any, error) {
	v, err := c.Get(path)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("config %s is not an array", path)
	}
	return items, nil
}

func splitPath(path string) ([]string, error) {
	if path == "" || path[0] != '.' {
		return nil, fmt.Errorf("config path must start with '.': %q", path)
	}
	if path == "." {
		return nil, nil
	}
	var segments []string
	rest := path[1:]
	for rest != "" {
		var seg string
		if rest[0] == '"' {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("unterminated quote in config path: %q", path)
			}
			seg = rest[1 : 1+end]
			rest = rest[2+end:]
		} else {
			end := strings.IndexByte(rest, '.')
			if end < 0 {
				end = len(rest)
			}
			seg = rest[:end]
			rest = rest[end:]
		}
		if seg == "" {
			return nil, fmt.Errorf("empty segment in config path: %q", path)
		}
		segments = append(segments, seg)
		if rest != "" {
			if rest[0] != '.' {
				return nil, fmt.Errorf("unsupported config path: %q", path)
			}
			rest = rest[1:]
		}
	}
	return segments, nil
}

// Raw formats a value the way `jq -r` prints it: strings bare, scalars as
// written, null as `null`, and anything structured as indented JSON. Object
// keys come out sorted, where jq keeps the file's order; nothing reads an
// object through this path, and a deterministic order beats a faithful one
// for a thing that exists to be asserted on.
func Raw(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(out)
	}
}

// StackMissing is the message for a configured stack that is not a directory.
//
// The defaults name three stacks, so a repository with different ones meets
// this before it meets anything else. OpenTofu's own message for a directory
// it cannot enter names neither the config file nor the key that put the
// value there; this one says the key, the file that was read, and what
// belongs in it. Returned rather than printed: prepare dies with it and
// validate puts it in the report a requester reads, which are different
// streams.
func (c *Config) StackMissing(key, stack, repoRoot string) string {
	return fmt.Sprintf("config .stacks.%s names %q, which is not a directory in %s. "+
		"Set .stacks.%s in %s to the directories your OpenTofu stacks live in.",
		key, stack, orDefault(repoRoot, "this repository"), key,
		orDefault(c.File, ".github/falconet.json"))
}

// Keys lists an object's keys, sorted, for callers that want to iterate
// deterministically — the prompts map, for one.
func Keys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
