package router

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// The identity key is made once, sealed with the secrets key, and the same
// key (kid) comes back at the next start.
func TestLoadServerIdentity_PersistsSealed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAKTABA_STATE_DIR", dir)
	t.Setenv("MAKTABA_SERVER_IDENTITY_PRIVATE_PEM", "")
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	first := LoadServerIdentity(key, nil)
	if first == nil {
		t.Fatal("no identity")
	}
	second := LoadServerIdentity(key, nil)
	if second == nil || second.Active().Kid != first.Active().Kid {
		t.Fatalf("the identity changed across starts: %v", second)
	}
	kid := first.Active().Kid
	sealed, err := os.ReadFile(filepath.Join(dir, "identity", kid+".pem.sealed"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("PRIVATE KEY")) {
		t.Fatal("the private key is on disk in the clear")
	}
}

// Without a secrets key the server still signs, with a key for this process.
func TestLoadServerIdentity_NoSealKeyStillSigns(t *testing.T) {
	t.Setenv("MAKTABA_STATE_DIR", t.TempDir())
	t.Setenv("MAKTABA_SERVER_IDENTITY_PRIVATE_PEM", "")
	if s := LoadServerIdentity(nil, nil); s == nil || s.Active() == nil {
		t.Fatal("want an ephemeral identity")
	}
}
