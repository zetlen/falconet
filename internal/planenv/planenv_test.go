package planenv

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

const maxCount = 5000

func TestTheShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []Entry
		err  string // a substring of the error, or empty
	}{
		{"two strings", `{"B": "2", "A": "1"}`, []Entry{{"A", "1"}, {"B", "2"}}, ""},
		{"an empty object is valid", `{}`, []Entry{}, ""},
		{"whitespace around it", "  {\n \"TF_VAR_x\": \"y\" }\n", []Entry{{"TF_VAR_x", "y"}}, ""},
		{"an underscore-led name", `{"_X": ""}`, []Entry{{"_X", ""}}, ""},
		{"a multi-line value (a PEM)", `{"KEY": "-----BEGIN\nabc\n-----END\n"}`, []Entry{{"KEY", "-----BEGIN\nabc\n-----END\n"}}, ""},
		{"an array", `["A=1"]`, nil, "the top-level value is an array"},
		{"a string", `"A=1"`, nil, "the top-level value is a string"},
		{"a number", `42`, nil, "the top-level value is a number"},
		{"null", `null`, nil, "the top-level value is null"},
		{"a number value", `{"A": 1}`, nil, "value of A is a number"},
		{"a nested object", `{"A": {"B": "c"}}`, nil, "value of A is an object"},
		{"a null value", `{"A": null}`, nil, "value of A is null"},
		{"a boolean value", `{"A": true}`, nil, "value of A is a boolean"},
		{"an array value", `{"A": ["x"]}`, nil, "value of A is an array"},
		{"a key with a dash", `{"AWS-KEY": "x"}`, nil, `key "AWS-KEY" is not an environment-variable name`},
		{"a key starting with a digit", `{"1A": "x"}`, nil, `key "1A" is not an environment-variable name`},
		{"an empty key", `{"": "x"}`, nil, `key "" is not an environment-variable name`},
		{"a key with a space", `{"A B": "x"}`, nil, `key "A B" is not an environment-variable name`},
		{"a key with an equals sign", `{"A=B": "x"}`, nil, `key "A=B" is not an environment-variable name`},
		{"not JSON", `{"A": "x"`, nil, "is not valid JSON"},
		{"empty input", ``, nil, "is empty"},
		{"two documents", `{"A": "x"} {"B": "y"}`, nil, "more than one JSON value"},
	} {
		got, err := Parse([]byte(tc.raw))
		if tc.err == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
				continue
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: expected an error containing %q, got entries %v", tc.name, tc.err, got)
			continue
		}
		if !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err, tc.err)
		}
	}
}

// For any object whose values are strings and whose keys are names, the
// error path is never taken, every pair comes back, and nothing is in an
// order that depends on the map.
func TestEveryValidObjectRoundTrips(t *testing.T) {
	f := func(keys []uint16, values []string) bool {
		obj := map[string]string{}
		for i, k := range keys {
			v := ""
			if i < len(values) {
				v = values[i]
			}
			obj["V"+itoa(int(k))] = v
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			return false
		}
		got, err := Parse(raw)
		if err != nil {
			return false
		}
		if len(got) != len(obj) {
			return false
		}
		for i, e := range got {
			if obj[e.Name] != e.Value {
				return false
			}
			if i > 0 && got[i-1].Name >= e.Name {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// Whatever the value, the error for a bad KEY never contains the value, and
// the error for a bad VALUE never contains the value either — not even a
// piece of a long one.
func TestAnErrorNeverQuotesAValue(t *testing.T) {
	const marker = "SECRET-VALUE-MARKER"
	f := func(value string, badKey bool) bool {
		value += marker
		var raw []byte
		var err error
		if badKey {
			// A bad key carrying a secret value.
			raw, err = json.Marshal(map[string]string{"bad-key": value})
		} else {
			// A bad value shape carrying the secret inside.
			raw, err = json.Marshal(map[string]any{"GOOD": map[string]string{"inner": value}})
		}
		if err != nil {
			return false
		}
		_, perr := Parse(raw)
		if perr == nil {
			return false
		}
		return !strings.Contains(perr.Error(), value) && !strings.Contains(perr.Error(), marker)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}

// A syntax error says where, never what.
func TestASyntaxErrorSaysWhereNotWhat(t *testing.T) {
	raw := `{"A": "super-secret-token-value" oops}`
	_, err := Parse([]byte(raw))
	if err == nil {
		t.Fatal("expected a syntax error")
	}
	if strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "oops") || strings.Contains(err.Error(), "'") {
		t.Errorf("the syntax error quotes the input: %q", err)
	}
	if !strings.Contains(err.Error(), "at byte") {
		t.Errorf("the syntax error does not say where: %q", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The runner decodes %25, %0D and %0A in a command's value, so a line with a
// literal "%25" would have been masked as "%" and left visible.
func TestMasksEscapeWhatTheRunnerDecodes(t *testing.T) {
	got := Masks("a%b\r\nc%25d")
	want := []string{MaskPrefix + "a%25b%0D", MaskPrefix + "c%2525d"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Masks = %q, want %q", got, want)
	}
}

func TestMasks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  []string
	}{
		{"one line", "AKIA", []string{"::add-mask::AKIA"}},
		{"a PEM, one mask per line", "-----BEGIN\nabc\n-----END", []string{"::add-mask::-----BEGIN", "::add-mask::abc", "::add-mask::-----END"}},
		{"an empty line is not a secret", "a\n\nb", []string{"::add-mask::a", "::add-mask::b"}},
		{"a trailing newline adds no mask", "a\n", []string{"::add-mask::a"}},
		{"an empty value masks nothing", "", nil},
		{"only newlines mask nothing", "\n\n", nil},
		{"a carriage return stays on its line, in the runner's encoding", "a\r\nb", []string{"::add-mask::a%0D", "::add-mask::b"}},
	} {
		got := Masks(tc.value)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// For any value: there is exactly one mask per non-empty line, each is the
// prefix followed by that line in the runner's encoding, in order, and no
// mask spans a line — a mask with a newline in it would be two workflow
// commands, the second of them garbage — nor carries a raw '%', which the
// runner would decode into something other than the line.
func TestEveryNonEmptyLineIsMaskedOnceAndNothingElse(t *testing.T) {
	f := func(value string) bool {
		got := Masks(value)
		var want []string
		for _, line := range strings.Split(value, "\n") {
			if line != "" {
				want = append(want, MaskPrefix+maskEscape(line))
			}
		}
		if !reflect.DeepEqual(got, want) {
			return false
		}
		for _, m := range got {
			if strings.ContainsAny(m, "\r\n") || m == MaskPrefix {
				return false
			}
			// Every '%' left in the mask is the start of one of the three
			// escapes, never a raw one.
			body := strings.TrimPrefix(m, MaskPrefix)
			for i := 0; i < len(body); i++ {
				if body[i] == '%' && !(strings.HasPrefix(body[i:], "%25") || strings.HasPrefix(body[i:], "%0D") || strings.HasPrefix(body[i:], "%0A")) {
					return false
				}
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: maxCount}); err != nil {
		t.Error(err)
	}
}
