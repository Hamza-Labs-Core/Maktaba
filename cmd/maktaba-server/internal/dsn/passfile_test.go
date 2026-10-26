package dsn

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestSplitPasswordTakesItOut(t *testing.T) {
	for _, c := range []struct{ in, bare, line string }{
		{"postgres://maktaba:s3cret@127.0.0.1:54329/maktaba?sslmode=disable",
			"postgres://maktaba@127.0.0.1:54329/maktaba?sslmode=disable", "127.0.0.1:54329:maktaba:maktaba:s3cret"},
		// escapes: the URL's percent-encoding undone, the passfile's colon and backslash added
		{"postgresql://node_ab:p%3Aw%5Cd%40x@home.local/maktaba",
			"postgresql://node_ab@home.local/maktaba", `home.local:5432:maktaba:node_ab:p\:w\\d@x`},
		{"postgres://maktaba@127.0.0.1:54329/maktaba", "postgres://maktaba@127.0.0.1:54329/maktaba", ""},
		{"sqlite:///var/lib/maktaba/db.sqlite", "sqlite:///var/lib/maktaba/db.sqlite", ""},
	} {
		bare, line := SplitPassword(c.in)
		if bare != c.bare || line != c.line {
			t.Errorf("%s: %q %q, want %q %q", c.in, bare, line, c.bare, c.line)
		}
	}
}

// Every *DATABASE_URL loses its password, PGPASSFILE names a 0600 file holding them, and
// no variable keeps the secret.
func TestProcessEnvLeavesNoPasswordInTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	env := []string{
		"DATABASE_URL=postgres://maktaba:home-secret@127.0.0.1:54329/maktaba",
		"MAKTABA_DATABASE_URL=postgres://maktaba:home-secret@127.0.0.1:54329/maktaba",
		"MAKTABA_COPY_DATABASE_URL=postgres://reader:copy-secret@127.0.0.1:54330/maktaba",
		"PGPASSFILE=/somewhere/else",
		"HOME=/Users/x",
	}
	out, err := ProcessEnv(env, dir)
	if err != nil {
		t.Fatal(err)
	}
	var passfile string
	for _, e := range out {
		if strings.Contains(e, "home-secret") || strings.Contains(e, "copy-secret") {
			t.Errorf("a secret left in %q", e)
		}
		if v, ok := strings.CutPrefix(e, "PGPASSFILE="); ok {
			if passfile != "" {
				t.Errorf("PGPASSFILE twice")
			}
			passfile = v
		}
	}
	if passfile == "" || !strings.HasPrefix(passfile, dir) {
		t.Fatalf("PGPASSFILE %q", passfile)
	}
	b, err := os.ReadFile(passfile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != "127.0.0.1:54329:maktaba:maktaba:home-secret\n127.0.0.1:54330:maktaba:reader:copy-secret\n" {
		t.Errorf("passfile:\n%s", got)
	}
	if st, _ := os.Stat(passfile); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("passfile mode %v", st.Mode().Perm())
	}
	// the same secrets, the same file (processes started together never clash)
	again, _ := ProcessEnv(env, dir)
	if again[len(again)-1] != "PGPASSFILE="+passfile {
		t.Errorf("another file for the same secrets: %v", again)
	}
}

func TestProcessEnvWithoutPasswordsChangesNothing(t *testing.T) {
	env := []string{"DATABASE_URL=postgres://maktaba@127.0.0.1:54329/maktaba", "PGPASSFILE=/mine"}
	out, err := ProcessEnv(env, t.TempDir())
	if err != nil || strings.Join(out, "|") != strings.Join(env, "|") {
		t.Fatalf("%v %v", out, err)
	}
}
