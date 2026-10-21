package nodeauth

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
)

// keep writes the rig's answers to path as the node's Keep loop does (its last save).
func (g *offlineRig) keep(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.r.Keep(ctx, path); close(done) }()
	cancel()
	<-done
}

// restarted is the same node after a restart in the outage: a new process at `at`, the
// home gone, its kept answers loaded.
func restarted(t *testing.T, at time.Time, window time.Duration, path string) *offlineRig {
	t.Helper()
	g := newOfflineRig(t, window)
	g.now = at
	g.home.setDown(true)
	g.r.LoadKept(path)
	return g
}

func TestKeptAnswersServeAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node", AnswersFile)
	g := newOfflineRig(t, 10*time.Minute)
	g.do(http.MethodGet, "/api/videos", "s1")
	g.do(http.MethodGet, "/api/videos", "pat_1")
	g.do(http.MethodGet, "/api/videos", "never-confirmed") // a refusal is not kept
	g.do(http.MethodGet, "/api/videos", "revoked")
	g.r.Evict(delegation.Revoked{SessionSHA256: []string{SessionKey("revoked")}})
	g.keep(t, path)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	kept, _ := io.ReadAll(zr)
	if !strings.Contains(string(kept), SessionKey("s1")) || strings.Contains(string(kept), `"s1"`) ||
		strings.Contains(string(kept), "pat_1") {
		t.Fatalf("the kept file names a credential, not its hash: %s", kept)
	}

	// the home goes away, the node restarts two minutes later
	n := restarted(t, g.now.Add(2*time.Minute), 10*time.Minute, path)
	for _, c := range []struct{ method, path, cred string }{
		{http.MethodGet, "/api/videos", "s1"},
		{http.MethodGet, "/api/videos", "pat_1"},
		{http.MethodPost, "/api/stream/sessions", "s1"},
	} {
		if a := n.do(c.method, c.path, c.cred); a.code != http.StatusOK || !a.marked {
			t.Fatalf("%s %s (%s) after a restart in the outage: %+v", c.method, c.path, c.cred, a)
		}
	}
	if a := n.do(http.MethodGet, MePath, "s1"); a.code != http.StatusOK {
		t.Fatalf("/api/auth/me after a restart: %+v", a)
	} else if u, _ := a.body["user"].(map[string]any); u["username"] != "amal" {
		t.Fatalf("/api/auth/me body after a restart: %v", a.body)
	}
	for _, cred := range []string{"never-confirmed", "revoked"} {
		if a := n.do(http.MethodGet, "/api/videos", cred); !a.refusedRead() {
			t.Fatalf("%s after a restart: %+v, want 503 readable false", cred, a)
		}
	}
	// the home answers again: it is asked first, as before the restart
	n.home.setDown(false)
	n.r.answered()
	if a := n.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK || a.marked {
		t.Fatalf("the home back after a restart: %+v", a)
	}
}

// The window runs from the home's last answer, not from the restart.
func TestKeptAnswersKeepTheWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), AnswersFile)
	g := newOfflineRig(t, 10*time.Minute)
	g.do(http.MethodGet, "/api/videos", "s1")
	g.keep(t, path)
	n := restarted(t, g.now.Add(11*time.Minute), 10*time.Minute, path)
	if a := n.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("a restart past the window: %+v, want 503 readable false", a)
	}
	if a := n.do(http.MethodPost, "/api/stream/sessions", "s1"); a.code != http.StatusUnauthorized {
		t.Fatalf("playback after a restart past the window: %+v, want anonymous (401)", a)
	}
}

func TestKeptAnswersSkipExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), AnswersFile)
	g := newOfflineRig(t, 10*time.Minute)
	g.home.expires = g.now.Add(time.Minute)
	g.do(http.MethodGet, "/api/videos", "s1")
	g.keep(t, path)
	n := restarted(t, g.now.Add(2*time.Minute), 10*time.Minute, path)
	if a := n.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("a session that expired before the restart: %+v, want 503 readable false", a)
	}
}

func TestKeptAnswersUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), AnswersFile)
	if err := os.WriteFile(path, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := restarted(t, time.Now(), 10*time.Minute, path)
	if a := n.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("with an unreadable file: %+v", a)
	}
}
