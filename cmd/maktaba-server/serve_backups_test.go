package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// backupsEnv is storageEnv with two dumps in the install's backups folder, every
// disk a local one with room.
func backupsEnv(t *testing.T) (cfgPath, data, backups string) {
	t.Helper()
	cfgPath, data, _ = storageEnv(t)
	backups = filepath.Join(data, "backups")
	for _, n := range []string{"maktaba-20261025-030000.dump", "pre-v1-to-v2.dump"} {
		if err := os.MkdirAll(backups, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(backups, n), []byte("dump"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prev := storageDiskOf
	t.Cleanup(func() { storageDiskOf = prev })
	storageDiskOf = func(string) (storage.Disk, error) {
		return storage.Disk{Mount: "/", FreeBytes: 500 << 30, TotalBytes: 1000 << 30, Suitable: true, FSType: "apfs"}, nil
	}
	return cfgPath, data, backups
}

func backupsCall(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestConfigBackupsRoutes(t *testing.T) {
	cfgPath, data, backups := backupsEnv(t)
	c := &configAdmin{path: cfgPath, token: "tok"}
	h := c.adminHandler(http.NotFoundHandler())
	req := httptest.NewRequest("GET", "/config/backups", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}

	rec = backupsCall(h, "GET", "/config/backups", "")
	var doc backupsDoc
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	if doc.Dir != backups || len(doc.Dumps) != 2 || doc.Saved != (config.BackupsSection{}).Settings() || doc.Disk == nil {
		t.Errorf("doc %+v", doc)
	}

	// values out of range: refused, nothing written
	before, _ := os.ReadFile(cfgPath)
	rec = backupsCall(h, "PUT", "/config/backups", `{"hour":25,"keep":0}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "backups.hour") ||
		!strings.Contains(rec.Body.String(), "backups.keep") {
		t.Errorf("bad values %d %s", rec.Code, rec.Body)
	}
	if after, _ := os.ReadFile(cfgPath); !bytes.Equal(before, after) {
		t.Error("server.toml changed")
	}

	// a new folder with dumps in the old one: the choice is asked for
	newDir := filepath.Join(filepath.Dir(data), "Backups")
	rec = backupsCall(h, "PUT", "/config/backups", `{"dir":"`+newDir+`","keep":14}`)
	var ref backupsRefusal
	if rec.Code != http.StatusUnprocessableEntity || json.Unmarshal(rec.Body.Bytes(), &ref) != nil || ref.Dumps != 2 ||
		ref.From != backups || ref.Errors[0].Field != "backups.move_dumps" {
		t.Fatalf("choice %d %s", rec.Code, rec.Body)
	}
	// moved
	rec = backupsCall(h, "PUT", "/config/backups", `{"dir":"`+newDir+`","keep":14,"move_dumps":true,"update_keep":4}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
		t.Fatalf("PUT %d %s", rec.Code, rec.Body)
	}
	if doc.Dir != newDir || len(doc.Dumps) != 2 || doc.Saved.Keep != 14 || doc.UpdateKeep != 4 {
		t.Errorf("after the move %+v", doc)
	}
	if ds, _ := pgbundle.ListDumps(backups); len(ds) != 0 {
		t.Errorf("dumps left in the old folder: %v", ds)
	}
	cfg, _, _ := config.LoadFrom(cfgPath)
	if cfg.Backups.Dir != newDir || cfg.Backups.Keep != 14 || pgbundle.BackupsKeep(newDir, 3) != 4 {
		t.Errorf("saved %+v", cfg.Backups)
	}
	// a folder inside the database's own is refused
	rec = backupsCall(h, "PUT", "/config/backups", `{"dir":"`+filepath.Join(data, "postgres", "b")+`","move_dumps":false}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "database's own folder") {
		t.Errorf("inside postgres: %d %s", rec.Code, rec.Body)
	}

	// delete: a name that is no dump is not found; the dump an update may put back stays
	if rec := backupsCall(h, "DELETE", "/config/backups/dumps/..%2Fserver.toml", ""); rec.Code != http.StatusNotFound {
		t.Errorf("bad name %d %s", rec.Code, rec.Body)
	}
	if err := apppayload.WriteDatabase(data, apppayload.Database{Version: "v2", State: apppayload.DatabaseMigrating,
		Dump: filepath.Join(newDir, "pre-v1-to-v2.dump")}); err != nil {
		t.Fatal(err)
	}
	if rec := backupsCall(h, "DELETE", "/config/backups/dumps/pre-v1-to-v2.dump", ""); rec.Code != http.StatusConflict {
		t.Errorf("in use %d %s", rec.Code, rec.Body)
	}
	rec = backupsCall(h, "DELETE", "/config/backups/dumps/maktaba-20261025-030000.dump", "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || len(doc.Dumps) != 1 {
		t.Errorf("delete %d %s", rec.Code, rec.Body)
	}
}

// With no server running the command changes server.toml itself.
func TestBackupsCommandOffline(t *testing.T) {
	cfgPath, data, _ := backupsEnv(t)
	prev := backupsAsker
	backupsAsker = func() *restoreAsker { return nil }
	t.Cleanup(func() { backupsAsker = prev })
	cmd := newBackupsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"set", "--nightly", "off", "--every-days", "7", "--hour", "0", "--pre-update", "off"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := config.LoadFrom(cfgPath)
	s := cfg.Backups.Settings()
	if s.Nightly || s.EveryDays != 7 || s.Hour != 0 || s.PreUpdate {
		t.Errorf("saved %+v", s)
	}
	for _, w := range []string{"Scheduled dump:      off, every week from 00:00", "Warning: with no dump before an update",
		"2 dumps"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
	cmd = newBackupsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"set", "--dir", filepath.Join(filepath.Dir(data), "B")})
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "move them") {
		t.Errorf("a new folder without a choice: %v", err)
	}
	cmd = newBackupsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"set", "--nightly", "maybe"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Error("--nightly maybe was taken")
	}
}

// [backups] pre_update = "off": an update's start takes no dump (and says so).
func TestAppBundleNoDumpWhenPreUpdateIsOff(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	cfg.Backups.PreUpdate = "off"
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	var log bytes.Buffer
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), &log)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if d, m, _ := f.counts(); d != 0 || m != 1 {
		t.Fatalf("%d dumps, %d migrations", d, m)
	}
	if !strings.Contains(log.String(), "no dump before the migrations") {
		t.Errorf("log %s", log.String())
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseMigrated || rec.Dump != "" {
		t.Errorf("record %+v", rec)
	}
}
