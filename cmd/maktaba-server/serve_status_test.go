package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// Epic 53 story 53.03: the web port answers /status.json itself (never the SPA, never
// the API proxy), every group starting until the supervisor's board exists, then the
// board's document, with the API down.
func TestWebPortServesStatusJSON(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Listen = "127.0.0.1:1" // nothing answers: the API is down
	web := webHandler(cfg, nil)
	t.Cleanup(func() { serveStatus.Store(nil) })

	get := func() supervisor.StatusDoc {
		rec := httptest.NewRecorder()
		web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status.json", nil))
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
			rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status %d, %v", rec.Code, rec.Header())
		}
		var d supervisor.StatusDoc
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatalf("not the document: %s", rec.Body.String())
		}
		return d
	}
	serveStatus.Store(nil)
	if d := get(); d.V != 1 || d.Components["api"] != supervisor.StatusStarting {
		t.Fatalf("before the board: %+v", d)
	}
	sup := supervisor.New([]supervisor.Process{{Role: supervisor.RoleAPI, Cmd: exec.Command("true")}},
		supervisor.DefaultPolicy(), supervisor.WithLog(&strings.Builder{}))
	serveStatus.Store(supervisor.NewStatusBoard(sup))
	if d := get(); d.Components["api"] != supervisor.StatusStarting || d.Mode != "normal" {
		t.Fatalf("with the board: %+v", d)
	}
}
