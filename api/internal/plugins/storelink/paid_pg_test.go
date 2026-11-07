package storelink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// Story 54.19 §6 (the home's half): a paid package comes from a URL the store signs for this
// home, fetched naming this home; the store's refusal (entitlement_missing) is the answer; a
// URL anywhere but the CDN's /paid/<sha256> is never fetched; an unlinked home asks nothing.
func TestPaidPackage(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	sha := strings.Repeat("ab", 32)
	var cdnHits []string
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits = append(cdnHits, r.URL.Path+" "+r.Header.Get(PaidHomeHeader))
		_, _ = w.Write([]byte("the-package"))
	}))
	defer cdn.Close()
	answer := ""
	var asked []map[string]string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(b, &m)
		asked = append(asked, m)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "MKS-Home id=h_1, ") || r.URL.Path != "/v1/homes/h_1/downloads" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if answer == "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"entitlement_missing"}`))
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer store.Close()
	l := &Link{DB: db, Secrets: secrets.NewMemory(), StoreURL: store.URL}

	// unlinked: nothing asked
	var e *Error
	if _, err := l.PaidPackage(ctx, cdn.URL, "dev.example.pro", sha, 1<<20); !errors.As(err, &e) || e.Code != CodeNotLinked || len(asked) != 0 {
		t.Fatalf("unlinked: %v", err)
	}
	_ = l.Secrets.Put(ctx, KeyName, bytes.Repeat([]byte{7}, ed25519.SeedSize), "test")
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, '{"home_id":"h_1","linked_at":"2026-11-06T00:00:00Z"}')`, StateKey); err != nil {
		t.Fatal(err)
	}
	// not bought: the store's code
	if _, err := l.PaidPackage(ctx, cdn.URL, "dev.example.pro", sha, 1<<20); !errors.As(err, &e) || e.Code != "entitlement_missing" || e.Status != http.StatusForbidden {
		t.Fatalf("not bought: %v", err)
	}
	if asked[0]["plugin_id"] != "dev.example.pro" || asked[0]["sha256"] != sha || len(asked[0]) != 2 {
		t.Fatalf("asked %v", asked[0])
	}
	// a URL elsewhere is never fetched
	for _, u := range []string{"https://evil.example/paid/" + sha + "?t=x", cdn.URL + "/paid/" + strings.Repeat("cd", 32) + "?t=x", cdn.URL + "/" + sha + ".mkplugin"} {
		answer = `{"url":"` + u + `"}`
		if _, err := l.PaidPackage(ctx, cdn.URL, "dev.example.pro", sha, 1<<20); !errors.As(err, &e) || e.Code != CodeUnreachable {
			t.Fatalf("%s: %v", u, err)
		}
	}
	if len(cdnHits) != 0 {
		t.Fatalf("fetched %v", cdnHits)
	}
	// bought: the file, fetched naming this home
	answer = `{"url":"` + cdn.URL + `/paid/` + sha + `?t=tok","expires_at":"2026-11-07T12:10:00Z"}`
	b, err := l.PaidPackage(ctx, cdn.URL, "dev.example.pro", sha, 1<<20)
	if err != nil || string(b) != "the-package" {
		t.Fatalf("bought: %q %v", b, err)
	}
	if len(cdnHits) != 1 || cdnHits[0] != "/paid/"+sha+" h_1" {
		t.Fatalf("CDN saw %v", cdnHits)
	}
	// offline: nothing asked
	n := len(asked)
	l.Offline = func() bool { return true }
	if _, err := l.PaidPackage(ctx, cdn.URL, "dev.example.pro", sha, 1<<20); err == nil || len(asked) != n {
		t.Fatalf("offline: %v, asked %d more", err, len(asked)-n)
	}
}
