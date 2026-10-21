package pgbundle

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// TestResolveAutoInsideAnAppPayload: "auto" runs the bundled Postgres inside
// any desktop app's payload (Epic 38 story 38.10: Windows too), whether the
// launcher named it or the app runs this maktaba-server from it directly.
func TestResolveAutoInsideAnAppPayload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("MAKTABA_CONFIG", "")
	t.Setenv("MAKTABA_POSTGRES_HOME", "")
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(tmp, "server")
	for _, f := range []string{filepath.Join("bin", exe("maktaba-server")), filepath.Join("postgres", "bin", exe("postgres")), "payload.json"} {
		p := filepath.Join(payload, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	server := filepath.Join(payload, "bin", exe("maktaba-server"))
	cfg := config.Defaults()
	if b, ok, err := Resolve(cfg, server); err != nil || !ok || b.Home != filepath.Join(payload, "postgres") {
		t.Fatalf("inside the payload (payload.json beside bin): %+v, %v, %v", b, ok, err)
	}
	// the launcher's MAKTABA_APP_BUNDLE, from wherever this maktaba-server is
	if err := os.Remove(filepath.Join(payload, "payload.json")); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Resolve(cfg, server); ok != (runtime.GOOS == "darwin") {
		t.Fatalf("outside a payload: ok=%v", ok)
	}
	t.Setenv("MAKTABA_APP_BUNDLE", payload)
	if b, ok, err := Resolve(cfg, ""); err != nil || !ok || b.Home != filepath.Join(payload, "postgres") {
		t.Fatalf("MAKTABA_APP_BUNDLE: %+v, %v, %v", b, ok, err)
	}
	// still: a [database] url wins, and so does "off"
	set := cfg
	set.DatabaseURLSet = true
	if _, ok, _ := Resolve(set, ""); ok {
		t.Error("a [database] url in server.toml must win")
	}
	off := cfg
	off.Postgres.Enabled = "off"
	if _, ok, _ := Resolve(off, ""); ok {
		t.Error("enabled = off must not run it")
	}
}
