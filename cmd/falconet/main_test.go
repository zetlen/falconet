package main

import (
	"strings"
	"testing"
)

// The dispatcher's whole knowledge of what exists is the two lists usage and
// known read, and native is what this binary answers for. Through the port
// those could differ: a name known to the lists and absent from native was
// handed to its bash script by the fallback, which is how every commit of
// the port stayed green against the whole suite (ADR-0006 D3 step 2). The
// scripts and the fallback went together in #19, so the three must now be
// one set — or a verb is known, accepted at the door, and unimplemented
// behind it, which from outside is exit 1 for a reason nobody can see.
func TestEveryKnownVerbIsNativeAndEveryNativeVerbIsKnown(t *testing.T) {
	listed := map[string]string{}
	for _, v := range verbs {
		if _, dup := listed[v]; dup {
			t.Errorf("%s appears twice in verbs", v)
		}
		listed[v] = "verbs"
	}
	for _, v := range unlisted {
		if where, dup := listed[v]; dup {
			t.Errorf("%s is in both %s and unlisted", v, where)
		}
		listed[v] = "unlisted"
	}
	for v, where := range listed {
		if _, ok := native[v]; !ok {
			t.Errorf("%s is known (%s) and has no native entry: known and unimplemented", v, where)
		}
	}
	for v := range native {
		if _, ok := listed[v]; !ok {
			t.Errorf("%s is native and in neither verbs nor unlisted: implemented and unreachable", v)
		}
	}
}

// known reads the lists and nothing else — not native, which is what let a
// verb be reachable without being listed anywhere.
func TestKnownIsTheTwoLists(t *testing.T) {
	for _, v := range verbs {
		if !known(v) {
			t.Errorf("%s is in verbs and not known", v)
		}
	}
	for _, v := range unlisted {
		if !known(v) {
			t.Errorf("%s is in unlisted and not known", v)
		}
	}
	for _, v := range []string{"", "park", "help", "nosuch", "-h"} {
		if known(v) {
			t.Errorf("%q is known", v)
		}
	}
}

// Usage names every listed verb and none of the unlisted ones.
// dispatcher.test.sh holds the same thing across the process boundary for
// prompt and plan-env; this holds it for the lists as a whole, so a name
// added to unlisted cannot be pasted into the usage text by habit.
func TestUsageListsTheVerbsAndNotTheUnlisted(t *testing.T) {
	for _, v := range verbs {
		if !strings.Contains(usageText, "\n  "+v+" ") {
			t.Errorf("usage does not list %s", v)
		}
	}
	for _, v := range unlisted {
		if strings.Contains(usageText, "\n  "+v+" ") {
			t.Errorf("usage lists the unlisted %s", v)
		}
	}
}
