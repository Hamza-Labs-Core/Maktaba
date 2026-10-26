package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No process the supervisor starts gets the database password in its environment: the
// URLs come without it and PGPASSFILE names the keys folder's 0600 file that holds it
// (a URL is printed by help texts, errors and logs; maktaba-server adduser --help once
// printed the live one).
func TestChildrenGetNoPasswordInTheirURLs(t *testing.T) {
	cfg := indexConfig(t)
	keys := t.TempDir()
	t.Setenv("MAKTABA_CONFIG", filepath.Join(keys, "server.toml"))
	cfg.Database.URL = "postgres://maktaba:the-secret@127.0.0.1:54329/maktaba?sslmode=disable"
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		env, err := ChildEnv(role, cfg)
		if err != nil {
			t.Fatal(err)
		}
		var passfile string
		for _, e := range env {
			if strings.Contains(e, "the-secret") {
				t.Errorf("%s: the password in %q", role, e)
			}
			if v, ok := strings.CutPrefix(e, "PGPASSFILE="); ok {
				passfile = v
			}
		}
		if filepath.Dir(passfile) != keys {
			t.Fatalf("%s: PGPASSFILE %q, want one in the keys folder", role, passfile)
		}
		if b, err := os.ReadFile(passfile); err != nil || !strings.Contains(string(b), ":maktaba:the-secret") {
			t.Fatalf("%s: passfile %q: %v", role, b, err)
		}
	}
}
