package gitea

import (
	"strings"
	"testing"
)

func TestCheckEnv(t *testing.T) {
	for _, tc := range []struct {
		name, url, bot string
		want           string // "" admits; otherwise a fragment of the refusal
	}{
		{"an instance and a login", "https://got.example/api/v1", "falconet-bot", ""},
		{"a test's loopback server", "http://127.0.0.1:3000/api/v1", "falconet-bot", ""},
		// An unset GITHUB_API_URL means api.github.com to every other
		// reader of it, and the self-check would send the token there.
		{"no API URL", "", "falconet-bot", "GITHUB_API_URL"},
		{"a blank API URL", "  ", "falconet-bot", "GITHUB_API_URL"},
		{"an API URL the token may not travel to", "http://got.example/api/v1", "falconet-bot", "not https"},
		{"no login", "https://got.example/api/v1", "", "FALCONET_BOT_LOGIN"},
		{"a login with a slash", "https://got.example/api/v1", "a/b", "FALCONET_BOT_LOGIN"},
		{"a GitHub App's login", "https://got.example/api/v1", "falconet[bot]", "FALCONET_BOT_LOGIN"},
		{"a dot segment", "https://got.example/api/v1", ".", "FALCONET_BOT_LOGIN"},
		{"a dot-dot segment", "https://got.example/api/v1", "..", "FALCONET_BOT_LOGIN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckEnv(tc.url, tc.bot)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("CheckEnv(%q, %q) = %v, want nil", tc.url, tc.bot, err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("CheckEnv(%q, %q) = %v, want an error containing %q", tc.url, tc.bot, err, tc.want)
			}
		})
	}
}
