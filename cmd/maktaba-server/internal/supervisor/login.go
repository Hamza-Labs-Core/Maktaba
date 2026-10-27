package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"
)

// LoginOf is a fingerprint of the database login an environment gives a process: its
// *DATABASE_URL values (without their passwords: dsn.ProcessEnv takes them out) and the
// passfile they are in (PGPASSFILE), as it is now, hashed; "" when it names no passfile. A TV
// process outlives a supervisor (the handover) with the login it started with, read once:
// one whose login is not its successor's (a machine's access re-made by its home since: a
// new password) would be refused by the home's database for as long as it ran, so it is
// not adopted.
func LoginOf(env []string) string {
	var parts []string
	passfile := false
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch {
		case strings.HasSuffix(k, "DATABASE_URL"):
			parts = append(parts, k+"="+v)
		case k == "PGPASSFILE" && v != "":
			passfile = true
			if b, err := os.ReadFile(v); err == nil {
				parts = append(parts, "passfile="+string(b))
			} else {
				parts = append(parts, "passfile at "+v)
			}
		}
	}
	if !passfile {
		// no password the supervisor handed it (dsn.ProcessEnv puts every one in a
		// passfile): nothing of its login can go stale under it
		return ""
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:16])
}

// login is the login p is started with now (LoginOf its environment).
func (p Process) login() string {
	if p.Cmd == nil {
		return ""
	}
	return LoginOf(p.Cmd.Env)
}
