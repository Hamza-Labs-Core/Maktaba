// Package hostcheck answers setup's `check` step for what is particular to a
// platform (Epic 39 story 39.6: Linux; specs/epics/39-linux-app/README.md,
// "Supported systems"). The step itself (Epic 38 story 38.6) adds these to
// what it reports for every platform (RAM, disk, what will run).
package hostcheck

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
)

// MinGlibc is the newest glibc any file of the Linux payload needs
// (tools/release/linux-app/pins.env MAX_GLIBC: the ffmpeg and tauri builds).
const MinGlibc = "2.35"

// Result is what the platform adds to the check.
type Result struct {
	// Problems keep the computer from running the server (`ok: false`).
	Problems []string
	// Warnings do not.
	Warnings []string
}

// Linux are the facts a Linux check reads (tests replace them).
type Linux struct {
	GOARCH string
	// Glibc is the C library's version ("" when unknown: not glibc, e.g. musl).
	Glibc func(ctx context.Context) string
	// Manager is the user's systemd manager.
	Manager service.UserManager
	// Unit is the app's unit (its file tells whether another install holds the name).
	Unit service.AppUnit
}

// LocalLinux reads this machine.
func LocalLinux(goarch string, unit service.AppUnit) Linux {
	return Linux{GOARCH: goarch, Glibc: localGlibc, Manager: service.LocalUserManager(), Unit: unit}
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
	switch l.GOARCH {
	case "amd64", "arm64":
	default:
		r.Problems = append(r.Problems, fmt.Sprintf("Maktaba runs on x86_64 and aarch64 Linux, not %s.", l.GOARCH))
	}
	switch v := l.Glibc(ctx); {
	case v == "":
		r.Problems = append(r.Problems, "This Linux has no GNU C library (glibc), which the bundled Postgres, ffmpeg and Python need (Alpine and other musl systems cannot run them).")
	case olderThan(v, MinGlibc):
		r.Problems = append(r.Problems, fmt.Sprintf("This Linux has glibc %s; Maktaba needs %s or newer (Ubuntu 22.04, Debian 12, Fedora 36, RHEL 10 or later).", v, MinGlibc))
	}
	st := l.Manager.Status(ctx, l.Unit)
	switch {
	case st.Manager != "ok":
		r.Problems = append(r.Problems, "The server runs as your systemd user service, which this computer cannot start: "+st.Reason+".")
	case st.Taken != "":
		r.Problems = append(r.Problems, fmt.Sprintf("Another installation of Maktaba already owns the service %s (it runs %s). Remove it first (maktaba-server uninstall from that installation).", st.Unit, st.Taken))
	case !st.Linger:
		r.Warnings = append(r.Warnings, "Your services start only when you log in. Setup asks to let the server start at boot (linger); that may need an administrator's password.")
	}
	return r
}

// olderThan compares dotted versions numerically ("2.31" < "2.35" < "2.100").
func olderThan(v, min string) bool {
	a, b := strings.Split(v, "."), strings.Split(min, ".")
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
