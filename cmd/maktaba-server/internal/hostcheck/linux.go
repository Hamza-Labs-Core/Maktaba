// Package hostcheck answers setup's `check` step for what is particular to a
// platform (Epic 39 story 39.6: Linux; specs/epics/39-linux-app/README.md,
// "Supported systems"). The step itself (Epic 38 story 38.6) adds these to
// what it reports for every platform (the architecture, RAM, disk, what will
// run).
package hostcheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
)

// MinGlibc is the newest glibc any file of the Linux payload needs
// (tools/release/linux-app/pins.env MAX_GLIBC: the ffmpeg and tauri builds).
const MinGlibc = "2.35"

// Note is one finding: a code the setup screens translate
// (setup.check.note.<code>, with Params), and its English.
type Note struct {
	Code    string
	Message string
	// Params are key, value pairs (the text's {key}).
	Params []any
}

// Result is what the platform adds to the check.
type Result struct {
	// Problems keep the computer from running the server (`ok: false`).
	Problems []Note
	// Warnings do not.
	Warnings []Note
}

func (r *Result) problem(code, message string, params ...any) {
	r.Problems = append(r.Problems, Note{Code: code, Message: message, Params: params})
}

func (r *Result) warn(code, message string, params ...any) {
	r.Warnings = append(r.Warnings, Note{Code: code, Message: message, Params: params})
}

// Linux are the facts a Linux check reads (tests replace them).
type Linux struct {
	// Glibc is the C library's version ("" when unknown: not glibc, e.g. musl).
	Glibc func(ctx context.Context) string
	// Manager is the user's systemd manager.
	Manager service.UserManager
	// Unit is the app's unit (its file tells whether another install holds the name).
	Unit service.AppUnit
	// User is the login name the remedies name.
	User string
}

// LocalLinux reads this machine.
func LocalLinux(unit service.AppUnit) Linux {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return Linux{Glibc: localGlibc, Manager: service.LocalUserManager(), Unit: unit, User: name}
}

// localGlibc asks getconf (glibc's own), which a musl system does not answer.
func localGlibc(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "getconf", "GNU_LIBC_VERSION").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out)) // "glibc 2.35"
	if len(f) == 2 && f[0] == "glibc" {
		return f[1]
	}
	return ""
}

// Check is the Linux part of setup's check.
func (l Linux) Check(ctx context.Context) Result {
	var r Result
	switch v := l.Glibc(ctx); {
	case v == "":
		r.problem("glibc_missing", "This Linux has no GNU C library (glibc), which the bundled Postgres, ffmpeg and Python "+
			"need (Alpine and other musl systems cannot run them).")
	case olderThan(v, MinGlibc):
		r.problem("glibc_old", fmt.Sprintf("This Linux has glibc %s; Maktaba needs %s or newer (Ubuntu 22.04, Debian 12, "+
			"Fedora 36, RHEL 10 or later).", v, MinGlibc), "have", v, "need", MinGlibc)
	}
	st := l.Manager.Status(ctx, l.Unit)
	switch {
	case st.Manager != "ok":
		// a shell from sudo -iu or su has no user manager of its own: a login of
		// that user starts one, and linger keeps it at boot
		r.problem("user_manager_missing", fmt.Sprintf("The server runs as your systemd user service, which this computer "+
			"cannot start: %s. Log in as %s directly (over SSH or at the console, not through sudo or su), or have an "+
			"administrator run `loginctl enable-linger %s`, then run the setup again.", st.Reason, l.User, l.User),
			"reason", st.Reason, "user", l.User)
	case st.SystemUnit != "":
		r.problem("service_system_wide", fmt.Sprintf("A system-wide Maktaba service is installed (%s): it would be a second "+
			"server on the same ports. Its administrator removes it first (sudo apt remove maktaba-server, or sudo systemctl "+
			"disable --now maktaba-server).", st.SystemUnit), "unit", st.SystemUnit)
	case st.Taken != "":
		r.problem("service_taken", fmt.Sprintf("Another installation of Maktaba already owns the service %s (it runs %s). "+
			"Remove it first (maktaba-server uninstall from that installation).", st.Unit, st.Taken),
			"unit", st.Unit, "runs", st.Taken)
	case !st.Linger:
		r.warn("linger_off", "Your services start only when you log in. Setup asks to let the server start at boot (linger); "+
			"that may need an administrator's password.")
	}
	return r
}

// olderThan compares dotted versions numerically ("2.31" < "2.35" < "2.100").
func olderThan(v, least string) bool {
	a, b := strings.Split(v, "."), strings.Split(least, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := part(a, i), part(b, i)
		if x != y {
			return x < y
		}
	}
	return false
}

func part(p []string, i int) int {
	if i >= len(p) {
		return 0
	}
	n, _ := strconv.Atoi(p[i])
	return n
}
