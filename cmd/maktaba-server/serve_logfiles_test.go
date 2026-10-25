package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logfiles"
)

// The supervisor serves this machine's log files over a window to its own api (the local
// token), and a node answers the home's logs.files on the channel within a frame.
func TestLogFilesAdminAndCommand(t *testing.T) {
	dirs := logfiles.Dirs{Logs: t.TempDir(), Updates: t.TempDir()}
	if err := os.WriteFile(filepath.Join(dirs.Logs, "app.log"), []byte("2026-10-24T05:30:00Z update: 0.3.0 -> 0.3.1 failed: relaunch_failed\n"+
		"2026-10-24T07:00:00Z update: later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := logFilesAdmin(http.NotFoundHandler(), "tok", dirs)
	get := func(q, auth string) (int, string) {
		r := httptest.NewRequest(http.MethodGet, "/logs/files?"+q, nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if code, _ := get("", ""); code != http.StatusForbidden {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := get("", "Bearer nope"); code != http.StatusForbidden {
		t.Fatalf("a wrong token: %d", code)
	}
	if code, _ := get("files=../x", "Bearer tok"); code != http.StatusBadRequest {
		t.Fatalf("a bad file: %d", code)
	}
	code, body := get("files=app,launcher&until=2026-10-24T06:00:00Z", "Bearer tok")
	var res logfiles.Result
	if code != http.StatusOK || json.Unmarshal([]byte(body), &res) != nil || len(res.Entries) != 1 ||
		!strings.Contains(res.Entries[0].Msg, "relaunch_failed") || len(res.Files) != 2 || !res.Files[1].Missing {
		t.Fatalf("the window: %d %s", code, body)
	}
	// the other admin routes still answer
	r := httptest.NewRequest(http.MethodGet, "/processes", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("the base handler: %d", w.Code)
	}

	ok, detail := logFilesCommand(t.Context(), dirs, json.RawMessage(`{"files":"app","since":"2026-10-24T06:00:00Z"}`))
	if got, isRes := detail.(logfiles.Result); !ok || !isRes || len(got.Entries) != 1 || got.Entries[0].Msg != "update: later" {
		t.Fatalf("the command: %v %+v", ok, detail)
	}
	if ok, _ := logFilesCommand(t.Context(), dirs, json.RawMessage(`{"files":"/etc/passwd"}`)); ok {
		t.Fatal("a bad file over the channel")
	}
}
