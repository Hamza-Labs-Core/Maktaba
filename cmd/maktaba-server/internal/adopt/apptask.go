package adopt

import (
	"fmt"
	"strings"
)

// The app's own sign-in task (contract-agent; the shell's agent_windows.rs):
// one per user, `\Maktaba Server <SID>`, the SID of the user it runs as. Before
// each user had their own, every user's was `\Maktaba Server`: a task of that
// name that runs as this user is this user's old one, which the shell replaces
// once (replace_old_task) and which adopt treats as the app's own until then.
// Another user's task of that name is theirs, and never touched.
const (
	appTaskPrefix = `\Maktaba Server`
	OldAppTask    = `\Maktaba Server`
)

// User is the user this process runs as: their SID, and their name as Task
// Scheduler writes it (DOMAIN\name, from USERDOMAIN and USERNAME as the shell
// reads them).
type User struct {
	SID  string
	Name string
}

// AppTask is the app's sign-in task of the user u.
func AppTask(u User) string { return appTaskPrefix + " " + u.SID }

// taskUser is this user as Task Scheduler names them (the shell's task_user).
func taskUser(getenv func(string) string) string {
	name := getenv("USERNAME")
	if name == "" {
		return ""
	}
	if d := getenv("USERDOMAIN"); d != "" {
		return d + `\` + name
	}
	return name
}

// runsAs reports whether a task's principal is u: its UserId is u's SID, u's
// DOMAIN\name or u's bare name, ignoring ASCII case (the shell's runs_as).
func (u User) runsAs(userID string) bool {
	if userID == "" {
		return false
	}
	if eqFoldASCII(userID, u.SID) {
		return true
	}
	if u.Name == "" {
		return false
	}
	bare := u.Name[strings.LastIndexByte(u.Name, '\\')+1:]
	return eqFoldASCII(userID, u.Name) || eqFoldASCII(userID, bare)
}

func eqFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// appTasks are this user's app tasks: theirs by its name, and their task of
// the old shared name when there is one.
func appTasks(sys Sys) ([]string, error) {
	u, err := sys.User()
	if err != nil {
		return nil, fmt.Errorf("cannot read this user's SID: %w", err)
	}
	if u.SID == "" {
		return nil, fmt.Errorf("cannot read this user's SID")
	}
	names := []string{AppTask(u)}
	tasks, err := sys.Tasks()
	if err != nil {
		return nil, fmt.Errorf("list the scheduled tasks: %w", err)
	}
	for _, t := range tasks {
		if eqFoldASCII(t.Name, OldAppTask) && u.runsAs(t.UserID) {
			names = append(names, t.Name)
		}
	}
	return names, nil
}
