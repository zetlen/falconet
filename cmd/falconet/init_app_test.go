package main

import (
	"testing"
	"time"
)

// waitWord once trimmed "0s" and then "0m" off a Duration's String, which
// turned the default 10m into "1". By unit now, and held here.
func TestWaitWordSaysTheWholeDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Minute:             "10m",
		30 * time.Second:             "30s",
		40 * time.Second:             "40s",
		time.Minute + 30*time.Second: "1m30s",
		2 * time.Second:              "2s",
		time.Hour:                    "1h",
		90 * time.Minute:             "90m",
		1500 * time.Millisecond:      "1.5s",
	} {
		if got := waitWord(d); got != want {
			t.Errorf("waitWord(%v) = %q, want %q", d, got, want)
		}
	}
}
