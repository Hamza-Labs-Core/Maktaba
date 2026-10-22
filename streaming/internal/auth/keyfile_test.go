package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeStreamKey(t *testing.T, path string) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	kid, _, err := ReadKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return kid
}

// The API makes the stream key as it starts, maybe after this process looked for it:
// the first signed URL that names it is verified at once, not at the next look (the
// looks are an hour apart here), and a key the file holds since is read on its first
// use too.
func TestAStreamKeyMadeAfterTheStartVerifiesAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stream-signing.key")
	var loads []string
	c := NewKeyFileCache(t.Context(), path, time.Hour, func(kid string) { loads = append(loads, kid) })
	if c.Lookup("none-yet") != nil || c.AnyKey() != nil {
		t.Fatal("a key before the file is there")
	}
	kid := writeStreamKey(t, path)
	time.Sleep(reloadEvery) // past the last miss's read
	if c.Lookup(kid) == nil {
		t.Fatal("the key made since is not read on its first use")
	}
	// the same key again: read once, said once
	c.Lookup(kid)
	if len(loads) != 1 || loads[0] != kid {
		t.Fatalf("loads %v", loads)
	}
	// the file replaced (a new key): its first token reads it
	next := writeStreamKey(t, path)
	time.Sleep(reloadEvery)
	if c.Lookup(next) == nil || len(loads) != 2 {
		t.Fatalf("a key replaced in the file: %v", loads)
	}
	// a flood of unknown keys reads the file at most every reloadEvery
	before := c.lastReload.Load()
	for range 50 {
		c.Lookup("unknown")
	}
	if after := c.lastReload.Load(); after != before && time.Duration(after-before) < reloadEvery {
		t.Fatal("reloads closer than reloadEvery")
	}
}
