package nodeauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalSessionsKeepOnlyHashes(t *testing.T) {
	now := time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), LocalSessionsFile)
	s := &LocalSessions{Path: path, Now: func() time.Time { return now }}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	id, csrf, expires, err := s.Create("u1", "amal", time.Hour)
	if err != nil || !strings.HasPrefix(id, LocalSessionPrefix) || csrf == "" || !expires.Equal(now.Add(time.Hour)) {
		t.Fatalf("create: %q %q %v %v", id, csrf, expires, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), id) || strings.Contains(string(raw), csrf) {
		t.Fatal("the store keeps a session id or a CSRF token itself")
	}

	// another process (a restart) reads the same sessions
	again := &LocalSessions{Path: path, Now: func() time.Time { return now }}
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	user, csrfHash, ok := again.Lookup(id)
	if !ok || user != "u1" || csrfHash != sum(csrf) {
		t.Fatalf("lookup after a restart: %q %q %v", user, csrfHash, ok)
	}
	if _, _, ok := again.Lookup("nls_unknown"); ok {
		t.Fatal("an unknown id was found")
	}
	if _, _, ok := again.Lookup(strings.TrimPrefix(id, LocalSessionPrefix)); ok {
		t.Fatal("an id without the prefix was found")
	}

	// the home back: every session ends by the deadline; a later deadline does not
	// lengthen them
	if err := again.Deadline(now.Add(10 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := again.Deadline(now.Add(20 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if _, _, ok := again.Lookup(id); ok {
		t.Fatal("a session outlived the deadline")
	}
	if again.Len() != 0 {
		t.Fatalf("live sessions: %d", again.Len())
	}

	// a sign-out removes it; an expired one is not written back
	id2, _, _, _ := again.Create("u1", "amal", time.Hour)
	if err := again.End(id2); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := again.Lookup(id2); ok {
		t.Fatal("a signed-out session was found")
	}
	raw, _ = os.ReadFile(path)
	if string(raw) != "{}" {
		t.Fatalf("the store kept ended sessions: %s", raw)
	}
}
