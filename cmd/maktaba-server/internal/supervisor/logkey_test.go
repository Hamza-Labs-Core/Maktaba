package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 53 story 53.01: every role gets the log key's path; the key is made once,
// 0600, and kept (never replaced, even when it has the wrong size).
func TestChildEnvSharesTheLogKey(t *testing.T) {
	t.Setenv("MAKTABA_LOG_KEY", "")
	t.Setenv("MAKTABA_CONFIG", filepath.Join(t.TempDir(), "server.toml"))
	want := LogKeyPath()
	if filepath.Dir(want) != filepath.Dir(SecretsKeyPath()) {
		t.Fatalf("log key %s is not beside secrets.key", want)
	}
	cfg := config.Defaults()
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		if !hasKV(childEnvOK(role, cfg), "MAKTABA_LOG_KEY", want) {
			t.Errorf("role %s: MAKTABA_LOG_KEY not set", role)
		}
	}
	k1, err := os.ReadFile(want)
	if err != nil || len(k1) != logKeySize {
		t.Fatalf("key: %v (%d bytes)", err, len(k1))
	}
	if fi, _ := os.Stat(want); os.PathSeparator == '/' && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	_ = childEnvOK(RoleAPI, cfg)
	if k2, _ := os.ReadFile(want); !bytes.Equal(k1, k2) {
		t.Fatal("a second start must keep the key")
	}
	if err := os.WriteFile(want, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if EnsureLogKey(want) == nil {
		t.Fatal("a key of the wrong size must be an error")
	}
	if b, _ := os.ReadFile(want); string(b) != "short" {
		t.Fatal("a key of the wrong size must never be replaced")
	}
	if hasKV(childEnvOK(RoleAPI, cfg), "MAKTABA_LOG_KEY", want) {
		t.Fatal("with an unusable key the roles get none (they then write no user)")
	}
}
