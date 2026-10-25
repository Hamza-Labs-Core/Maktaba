package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
)

// Story 35.6: the home's supervisor asks its own API for the update's TV change with its
// local token, naming the release's streaming binary by its checksum.
func TestAPITVAsksItsOwnAPI(t *testing.T) {
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "supervisor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "supervisor", "local.token"), []byte("t0k"), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := "maktaba-streaming"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	bin := filepath.Join(t.TempDir(), exe)
	if err := os.WriteFile(bin, []byte("the streaming binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_STREAMING_BIN", bin)

	var mu sync.Mutex
	var seen []string
	var begin map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-Maktaba-Local-Token") != "t0k" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost && r.URL.Path == "/internal/updates/tv-change" {
			_ = json.NewDecoder(r.Body).Decode(&begin)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"outcome": "queued", "change_id": "c1", "number": 7})
	}))
	defer srv.Close()

	var cfg config.Config
	cfg.Server.DataDir = data
	cfg.Server.Listen = srv.Listener.Addr().String()
	tv := &apiTV{cfg: cfg}
	j := &updater.Journal{Job: updater.Job{RunID: "run-1", Version: "1.5.0", Tag: "v1.5.0", Home: true}}
	ctx := context.Background()
	st, err := tv.Begin(ctx, j)
	if err != nil || st.Outcome != updater.TVQueued || st.ChangeID != "c1" || st.Number != 7 {
		t.Fatalf("begin: %+v %v", st, err)
	}
	if begin["run_id"] != "run-1" || len(begin["sha256"]) != 64 || !strings.HasSuffix(begin["version"], "1.5.0") {
		t.Fatalf("asked for %v", begin)
	}
	if _, err := tv.Status(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := tv.Withdraw(ctx, j, "watch_timeout"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST /internal/updates/tv-change", "GET /internal/updates/tv-change/run-1",
		"POST /internal/updates/tv-change/run-1/withdraw"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("requests %v", seen)
	}
}
