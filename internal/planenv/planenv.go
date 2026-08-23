// Package planenv is the shape FALCONET_PLAN_ENV must have, checked once,
// here, by everything that handles the secret: `init`, which seals it, and
// the `plan-env` subcommand, which replaced the workflow's jq-driven
// "Credentials for the stacks that plan" step (#19) and validates the same
// secret with the same code before anything is exported from it. Masks is
// the other half of that step: what the runner must be told to redact.
//
// FALCONET_PLAN_ENV is one JSON object of environment variables — whatever
// the operator exports before `tofu init && tofu plan` in the stacks named
// in stacks.plan: backend keys, provider tokens, TF_VAR_*. The workflow
// masks every value and exports each pair into the two jobs that run tofu,
// and to no other (README step 5). So the shape is exactly: an object, every
// value a string, every key an environment-variable name.
//
// # What an error may say
//
// The value is a credential. Nothing here ever quotes, echoes, logs or
// returns any part of a VALUE — not in an error, not in a parse error's
// context, not on failure. An error names the key it is about, or the shape
// that was found where a string was expected, and that is all. The JSON
// decoder's own errors are not passed through either: a syntax error from
// encoding/json quotes the offending character, and one character of a
// secret is still a character of a secret.
//
// Nothing here touches the filesystem or the environment: the caller hands
// in bytes and gets back pairs, in a deterministic order, or an error that
// is safe to print.
package planenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Entry is one variable the secret sets.
type Entry struct {
	Name  string
	Value string
}

// name is what Actions accepts as a variable name, and what the workflow's
// export step checked with the same expression: an identifier.
var name = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse checks raw against the shape and returns its pairs sorted by name.
// An empty object is valid and yields no entries: README step 5 says "if
// every stack you plan needs no credentials at all, skip this step", and
// an operator who stores `{}` has said the same thing explicitly.
func Parse(raw []byte) ([]Entry, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, syntax(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("FALCONET_PLAN_ENV must be one JSON object, and there is more than one JSON value")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("FALCONET_PLAN_ENV must be a JSON object, and the top-level value is %s", kind(v))
	}
	entries := make([]Entry, 0, len(obj))
	for k, val := range obj {
		if !name.MatchString(k) {
			return nil, fmt.Errorf("FALCONET_PLAN_ENV key %q is not an environment-variable name (letters, digits and _, not starting with a digit)", k)
		}
		s, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("FALCONET_PLAN_ENV value of %s is %s, and every value must be a string", k, kind(val))
		}
		entries = append(entries, Entry{Name: k, Value: s})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// syntax is a JSON decoding error with the input's bytes left out. A
// *json.SyntaxError carries an offset, which is safe to say and useful; its
// message carries the character found there, which is not.
func syntax(err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Errorf("FALCONET_PLAN_ENV is not valid JSON (at byte %d)", se.Offset)
	}
	if errors.Is(err, io.EOF) {
		return errors.New("FALCONET_PLAN_ENV is empty, and must be one JSON object")
	}
	return errors.New("FALCONET_PLAN_ENV is not valid JSON")
}

// kind names a decoded value's JSON type, and nothing about its content.
func kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return "not a string"
}

// MaskPrefix is the workflow command that tells the runner to redact a
// string from every log line after it.
const MaskPrefix = "::add-mask::"

// Masks is the ::add-mask:: command for every non-empty line of value, in
// order. add-mask is per line, and a PEM is many lines: the runner masks
// exact strings, so a multi-line value masked as one string would mask
// nothing, and each line has to be named on its own. An empty line is not a
// secret, and the runner refuses to mask one anyway. Printed by plan-env
// BEFORE the value is written anywhere.
func Masks(value string) []string {
	var out []string
	for _, line := range strings.Split(value, "\n") {
		if line != "" {
			out = append(out, MaskPrefix+line)
		}
	}
	return out
}
