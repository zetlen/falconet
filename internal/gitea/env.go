package gitea

import (
	"errors"
	"fmt"
	"strings"
)

// CheckEnv refuses the environment a Gitea run is given, apiURL from
// $GITHUB_API_URL and bot from $FALCONET_BOT_LOGIN, before the verb reads an
// event file or builds a client.
func CheckEnv(apiURL, bot string) error {
	// Every other reader of GITHUB_API_URL takes an unset one to mean
	// api.github.com. The token is a Gitea administrator's, and the client's
	// first request sends it to the API it is given, so a Gitea run names its
	// instance or does not start.
	if strings.TrimSpace(apiURL) == "" {
		return errors.New("with forge set to gitea, GITHUB_API_URL names the instance's API, https://<instance>/api/v1, and it is unset")
	}
	if _, err := apiBase(apiURL); err != nil {
		return err
	}
	// Gitea's users carry no type, so the login of the user whose token the
	// verbs hold is what tells falconet's own events from a person's, and
	// the client checks it against the token before its first request. A run
	// that cannot tell falconet's own events apart reads no event at all.
	if !isLogin(bot) {
		return fmt.Errorf("with forge set to gitea, FALCONET_BOT_LOGIN names the user whose token GH_TOKEN is, and nothing else tells falconet's own events apart; it is %q", bot)
	}
	return nil
}
