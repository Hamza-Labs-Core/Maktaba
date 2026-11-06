package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 53 story 53.11 T08: Retention on a fresh home is 7/30/90 days and 1 GB with what each
// uses; a value out of range is 400 and nothing is saved; a change is saved in [logs] and
// the log index restarts to take it; detailed logs for an hour show until when.
func TestConfigLogsRoutes(t *testing.T) {
	cfgPath, data, _ := storageEnv(t)
	seg := filepath.Join(data, "logs", "segments", "api")
	if err := os.MkdirAll(seg, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(seg, "20261106-10.ndjson"), []byte(strings.Repeat("x", 1000)), 0o600)
	var restarted []string
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	c := &configAdmin{path: cfgPath, token: "tok", logs: &logsAdmin{
		restart: func(id string) int { restarted = append(restarted, id); return 1 },
		now:     func() time.Time { return now }}}
	h := c.adminHandler(http.NotFoundHandler())
	if rec := httptest.NewRecorder(); true {
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/config/logs", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("no token: %d", rec.Code)
		}
	}
	doc := func(rec *httptest.ResponseRecorder) logsDoc {
		t.Helper()
		var d logsDoc
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return d
	}
	d := doc(backupsCall(h, "GET", "/config/logs", ""))
	if d.LogsDays != 7 || d.ErrorsDays != 30 || d.ReportsDays != 90 || d.TotalBytes != 1<<30 || d.Used.Logs != 1000 {
		t.Fatalf("defaults: %+v", d)
	}
	if rec := backupsCall(h, "PUT", "/config/logs", `{"logs_days":91}`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "logs.logs_days") {
		t.Fatalf("out of range: %d %s", rec.Code, rec.Body)
	}
	if rec := backupsCall(h, "PUT", "/config/logs", `{"total_bytes":1024}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a cap under 256 MB: %d", rec.Code)
	}
	if len(restarted) != 0 {
		t.Fatal("a refused change restarts nothing")
	}
	d = doc(backupsCall(h, "PUT", "/config/logs", `{"logs_days":14,"errors_days":60,"total_bytes":2147483648}`))
	if d.LogsDays != 14 || d.ErrorsDays != 60 || d.ReportsDays != 90 || d.TotalBytes != 2<<30 {
		t.Fatalf("changed: %+v", d)
	}
	cfg, _, _ := config.LoadFrom(cfgPath)
	if cfg.Logs.Retention != "14d" || cfg.Logs.ErrorRetention != "60d" || cfg.Logs.MaxTotalSize != "2048MB" {
		t.Fatalf("saved: %+v", cfg.Logs)
	}
	if len(restarted) != 1 || restarted[0] != "logs.index" {
		t.Fatalf("restarted %v", restarted)
	}
	d = doc(backupsCall(h, "POST", "/config/logs/level", `{"service":"pipeline","level":"debug","minutes":60}`))
	if d.Detailed["pipeline"] != "2026-11-06T11:00:00Z" {
		t.Fatalf("detailed: %+v", d.Detailed)
	}
	if rec := backupsCall(h, "POST", "/config/logs/level", `{"service":"postgres","level":"debug"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("postgres: %d", rec.Code)
	}
	d = doc(backupsCall(h, "POST", "/config/logs/level", `{"service":"pipeline","level":"info"}`))
	if len(d.Detailed) != 0 {
		t.Fatalf("info ends it: %+v", d.Detailed)
	}
}
