package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/logs"
)

// The home's files come from its supervisor with the per-boot token; a supervisor from
// before /logs/files, or none, has no files; a machine needs its agent connected.
func TestLogFilesSource(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("t0k\n"), 0o600)
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs/files" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer t0k" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"entries":[],"files":[{"name":"` + r.URL.Query().Get("files") + `.log"}]}`))
	}))
	defer sup.Close()
	ctx := context.Background()
	raw, err := LogFiles(sup.URL, tok, nil, nil)(ctx, "", url.Values{"files": {"app"}})
	if err != nil || string(raw) != `{"entries":[],"files":[{"name":"app.log"}]}` {
		t.Fatalf("the home's files: %s %v", raw, err)
	}
	if _, err := LogFiles(sup.URL, filepath.Join(t.TempDir(), "none"), nil, nil)(ctx, "", url.Values{}); err == nil {
		t.Fatal("no token: taken")
	}
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	if _, err := LogFiles(old.URL, tok, nil, nil)(ctx, "", url.Values{}); !errors.Is(err, logs.ErrNoSupervisor) {
		t.Fatalf("an older supervisor: %v", err)
	}
	if _, err := LogFiles("", tok, nil, nil)(ctx, "", url.Values{}); !errors.Is(err, logs.ErrNoSupervisor) {
		t.Fatalf("no supervisor: %v", err)
	}
	if _, err := LogFiles(sup.URL, tok, &HubRef{}, nil)(ctx, "n1", url.Values{}); !errors.Is(err, logs.ErrNodeOffline) {
		t.Fatalf("a machine with no hub: %v", err)
	}
}
