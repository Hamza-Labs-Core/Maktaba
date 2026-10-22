package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
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
// use too; the key it held before still verifies the URLs it signed.
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
	if c.Lookup(kid) == nil {
		t.Fatal("the key before the file was replaced no longer verifies the URLs it signed")
	}
	// a flood of unknown keys reads the file at most every reloadEvery, and each
	// request that finds a read under way waits for it
	reads := func() time.Time { c.reloadMu.Lock(); defer c.reloadMu.Unlock(); return c.reloadedAt }
	before := reads()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { c.Lookup("unknown") })
	}
	wg.Wait()
	if after := reads(); !after.Equal(before) && after.Sub(before) < reloadEvery {
		t.Fatal("reloads closer than reloadEvery")
	}
	if c.Lookup(next) == nil {
		t.Fatal("the key the file holds")
	}
}

// A request that names a key not read yet while another's read is under way waits for
// that read, then finds the key (it is not refused for losing the race).
func TestARequestWaitsForTheReadUnderWay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stream-signing.key")
	kid := writeStreamKey(t, path)
	release := make(chan struct{})
	started := make(chan struct{})
	c := &JWKSCache{keys: map[string]*rsa.PublicKey{}}
	c.reload = func() bool {
		close(started)
		<-release
		k, pub, err := ReadKeyFile(path)
		if err != nil {
			return false
		}
		c.Set(map[string]*rsa.PublicKey{k: pub})
		return true
	}
	first := make(chan *rsa.PublicKey)
	go func() { first <- c.Lookup(kid) }()
	<-started
	second := make(chan *rsa.PublicKey)
	go func() { second <- c.Lookup(kid) }()
	time.Sleep(20 * time.Millisecond) // the second request is at the read's lock
	close(release)
	if <-first == nil || <-second == nil {
		t.Fatal("a request during the read was refused")
	}
}

// The key a replaced file held is dropped once the URLs it signed are past (a day; a
// moment here).
func TestTheReplacedKeyGoesOnceItsURLsArePast(t *testing.T) {
	was := replacedKeyKeep
	replacedKeyKeep = 400 * time.Millisecond
	t.Cleanup(func() { replacedKeyKeep = was })
	path := filepath.Join(t.TempDir(), "stream-signing.key")
	old := writeStreamKey(t, path)
	c := NewKeyFileCache(t.Context(), path, time.Hour, nil)
	next := writeStreamKey(t, path)
	time.Sleep(reloadEvery)
	if c.Lookup(next) == nil || c.Lookup(old) == nil {
		t.Fatal("both keys, right after the replacement")
	}
	time.Sleep(replacedKeyKeep + 300*time.Millisecond)
	if c.Lookup(old) != nil {
		t.Fatal("the replaced key still verifies past the URLs it signed")
	}
	if c.Lookup(next) == nil {
		t.Fatal("the key the file holds")
	}
}
