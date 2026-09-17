package forge

// RunnerMismatch is the reason the configured forge, kind, cannot be the one
// running this job, or "". giteaActions and githubActions are the values of
// $GITEA_ACTIONS and $GITHUB_ACTIONS.
//
// The forge key decides which reader turns the event file into who acted and
// whether it was falconet. GitHub's reader tells falconet's own events by
// `sender.type`, which a Gitea payload does not carry, and Gitea's reader, on
// a GitHub payload, finds no `changes.added_labels` and compares logins
// against a bot login GitHub never sends. So a job whose runner is one
// forge's, with the other forge configured, is refused before the event file
// is read or anything is sent, with a reason that names the key to change.
//
// act_runner sets GITEA_ACTIONS=true on every job, from the runner, and sets
// GITHUB_ACTIONS=true as well; GitHub Actions sets only GITHUB_ACTIONS. A
// GitHub runner is therefore GITHUB_ACTIONS without GITEA_ACTIONS.
func RunnerMismatch(kind, giteaActions, githubActions string) string {
	gitea := giteaActions == "true"
	github := githubActions == "true" && !gitea
	switch {
	case gitea && kind != "gitea":
		return "running under Gitea Actions with forge set to " + kind + ": set forge to gitea in the config"
	case github && kind == "gitea":
		return "running under GitHub Actions with forge set to gitea: set forge to github in the config, or run the verbs where Gitea's events are"
	}
	return ""
}

// OnARunner is whether this job runs on a GitHub or a Gitea Actions runner,
// from the values of $GITEA_ACTIONS and $GITHUB_ACTIONS: either one exactly
// "true". RunnerMismatch counts GITEA_ACTIONS alone as a Gitea runner, and a
// verb that refuses to act on a runner without an event reads the same two
// variables, so neither check calls a job a workstation that the other calls
// a runner.
func OnARunner(giteaActions, githubActions string) bool {
	return giteaActions == "true" || githubActions == "true"
}
