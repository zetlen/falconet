package prepare

import "testing"

func TestAssignee(t *testing.T) {
	for _, c := range []struct {
		name, flag, actor, sender, want string
	}{
		{"--assignee wins", "bob", "alice", "maint", "bob"},
		{"then the triggering actor", "", "alice", "maint", "alice"},
		{"then the event's sender", "", "", "maint", "maint"},
		{"and with none of them, nobody: the token's own login is the caller's to ask", "", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Assignee(c.flag, c.actor, c.sender); got != c.want {
				t.Errorf("Assignee(%q, %q, %q) = %q, want %q", c.flag, c.actor, c.sender, got, c.want)
			}
		})
	}
}
