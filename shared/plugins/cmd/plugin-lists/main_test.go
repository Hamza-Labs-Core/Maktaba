package main

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// #755: the release step embeds the store's current lists, verified, and fails closed;
// test keys only (made from public labels, no real build trusts them).

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func signer(label string) pluginsig.Signer {
	seed := sha256.Sum256([]byte("maktaba-plugins TEST KEY, never for production: " + label))
	return pluginsig.NewSigner(seed[:])
}

type kit struct {
	root, store pluginsig.Signer
	rootRaw     []byte
	files       map[string][]byte
	dir         string
	srv         *httptest.Server
}

func newKit(t *testing.T) *kit {
	t.Helper()
	k := &kit{root: signer("root-1"), store: signer("store-1"), files: map[string][]byte{}, dir: t.TempDir()}
	var err error
	k.rootRaw, err = pluginsig.SignEnvelope(pluginsig.KindRoot, pluginsig.RootSigned{Version: 1, Threshold: 1,
		Keys: []pluginsig.RootKey{{ID: k.root.ID, Public: k.root.Public()}}}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	k.keys(1, k.root, 300*24*time.Hour)
	k.revoked(5, k.store, 47*time.Hour)
	for _, f := range []string{"store-keys-embedded.json", "revoked-embedded.json"} {
		if err := os.WriteFile(filepath.Join(k.dir, f), []byte("null\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := k.files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(k.srv.Close)
	return k
}

func (k *kit) keys(version int64, by pluginsig.Signer, life time.Duration) {
	k.files["store-keys.json"], _ = pluginsig.SignEnvelope(pluginsig.KindStoreKeys, pluginsig.StoreKeysSigned{Version: version,
		Expires: pluginsig.FormatTime(now.Add(life)), Keys: []pluginsig.StoreKey{{ID: k.store.ID, Public: k.store.Public(),
			Use: pluginsig.UseStore, NotBefore: pluginsig.FormatTime(now.Add(-30 * 24 * time.Hour)),
			NotAfter: pluginsig.FormatTime(now.Add(365 * 24 * time.Hour))}}}, by)
}

func (k *kit) revoked(version int64, by pluginsig.Signer, life time.Duration) {
	issued := now.Add(-time.Hour)
	if life < 0 {
		issued = now.Add(life - 47*time.Hour)
	}
	k.files["revoked.json"], _ = pluginsig.SignEnvelope(pluginsig.KindRevoked, pluginsig.RevokedSigned{Version: version,
		Issued: pluginsig.FormatTime(issued), Expires: pluginsig.FormatTime(issued.Add(47 * time.Hour)),
		Entries: []pluginsig.RevocationEntry{}}, by)
}

func (k *kit) run() (string, error) {
	return run(context.Background(), k.rootRaw, k.srv.URL, k.dir, &http.Client{Timeout: 5 * time.Second}, now)
}

func (k *kit) embedded(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(k.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestEmbedsTheVerifiedLists(t *testing.T) {
	k := newKit(t)
	msg, err := k.run()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "store-keys.json version 1") || !strings.Contains(msg, "revoked.json version 5") {
		t.Fatalf("message %q", msg)
	}
	if k.embedded(t, "store-keys-embedded.json") != string(k.files["store-keys.json"]) ||
		k.embedded(t, "revoked-embedded.json") != string(k.files["revoked.json"]) {
		t.Fatal("not embedded as fetched")
	}
}

// Each failure stops the release with an error naming it, and leaves the committed files.
func TestFailsClosed(t *testing.T) {
	cases := map[string]struct {
		change func(k *kit)
		want   string
	}{
		"the CDN unreachable":          {func(k *kit) { k.srv.Close() }, "could not be reached"},
		"store-keys.json missing":      {func(k *kit) { delete(k.files, "store-keys.json") }, "answered 404 for store-keys.json"},
		"revoked.json missing":         {func(k *kit) { delete(k.files, "revoked.json") }, "answered 404 for revoked.json"},
		"store keys signed by another": {func(k *kit) { k.keys(1, signer("evil"), 300*24*time.Hour) }, "not signed by a key of the plugin root"},
		"store keys expired":           {func(k *kit) { k.keys(1, k.root, -time.Hour) }, "expired"},
		"revoked signed by another":    {func(k *kit) { k.revoked(5, signer("evil"), 47*time.Hour) }, "revoked.json"},
		"revoked expired":              {func(k *kit) { k.revoked(5, k.store, -time.Hour) }, "expired"},
		"revoked older than committed": {func(k *kit) {
			_ = os.WriteFile(filepath.Join(k.dir, "revoked-embedded.json"), []byte(`{"signed":{"version":9}}`), 0o644)
		}, "older than 9"},
		"store keys older than committed": {func(k *kit) {
			_ = os.WriteFile(filepath.Join(k.dir, "store-keys-embedded.json"), []byte(`{"signed":{"version":3}}`), 0o644)
		}, "older than 3"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			k := newKit(t)
			c.change(k)
			before := k.embedded(t, "revoked-embedded.json")
			if _, err := k.run(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error with %q", err, c.want)
			}
			if k.embedded(t, "revoked-embedded.json") != before {
				t.Fatal("a refused list was written")
			}
		})
	}
}

// While the committed root lists no keys (the store is not live), nothing is fetched or
// written, and the release goes on.
func TestNoRootKeysEmbedsNothing(t *testing.T) {
	k := newKit(t)
	hit := false
	k.srv.Config.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true })
	// the committed placeholder's form until the owner makes the root keys
	empty := []byte(`{"signatures":[],"signed":{"keys":[],"threshold":1,"version":0}}`)
	msg, err := run(context.Background(), empty, k.srv.URL, k.dir, &http.Client{Timeout: time.Second}, now)
	if err != nil || !strings.Contains(msg, "no keys") || hit {
		t.Fatalf("%q %v (fetched %v)", msg, err, hit)
	}
	if k.embedded(t, "store-keys-embedded.json") != "null" {
		t.Fatal("written")
	}
}
