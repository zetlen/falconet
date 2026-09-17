package prepare

// Assignee is who the claim is recorded against: the first of --assignee,
// $GITHUB_TRIGGERING_ACTOR and the event's sender that is set, or "" when
// none is, and the caller asks the forge for the token's own login.
//
// The claim names the person who asked for the run where one is known.
// GitHub Actions names that person in GITHUB_TRIGGERING_ACTOR; act, the
// runner behind Gitea Actions, sets no such variable, and the event's sender
// is, by the time the claim is made, the account the sender rule admitted.
// A run with none of the three asks for the token's own login: on GitHub an
// App token has none to give, and on Gitea the token is the bot user's, so a
// workstation run on Gitea with no event and no --assignee assigns the bot.
func Assignee(flag, triggeringActor, sender string) string {
	for _, who := range []string{flag, triggeringActor, sender} {
		if who != "" {
			return who
		}
	}
	return ""
}
