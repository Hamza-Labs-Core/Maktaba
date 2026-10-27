package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// storageEnv is an install in a temp dir whose data folder the owner chose
// (data_dir: the state root too), with the supervisor's admin on a free port.
func storageEnv(t *testing.T) (cfgPath, data, admin string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	t.Setenv("MAKTABA_POSTGRES_HOME", "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	admin = ln.Addr().String()
	_ = ln.Close()
	cfgPath, data = filepath.Join(dir, "cfg", "server.toml"), filepath.Join(dir, "Big", "Maktaba")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# mine\n[server]\ndata_dir = '" + data + "'\n\n[supervisor]\nadmin_addr = '" + admin + "'\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_CONFIG", cfgPath)
	if err := config.MarkStorage(filepath.Dir(cfgPath), data, "setup"); err != nil {
		t.Fatal(err)
	}
	return cfgPath, data, admin
}

func processesAt(addr string) (supervisor.Snapshot, error) {
	var s supervisor.Snapshot
	resp, err := http.Get("http://" + addr + "/processes")
	if err != nil {
		return s, err
	}
	defer func() { _ = resp.Body.Close() }()
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// The disk of the data folder is not connected: serve waits, says so on
// /processes (storage_waiting), makes nothing in its place, and starts once it is back.
func TestServeWaitsForTheDataDisk(t *testing.T) {
	cfgPath, data, admin := storageEnv(t)
	prev := storagePoll
	storagePoll = 20 * time.Millisecond
	t.Cleanup(func() { storagePoll = prev })
	if err := os.RemoveAll(filepath.Dir(data)); err != nil { // unplugged
		t.Fatal(err)
	}
	cfg, _, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := storageGate(cfg, "", io.Discard)
		done <- err
	}()
	var snap supervisor.Snapshot
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err = processesAt(admin)
		if err == nil && len(snap.Processes) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no /processes: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	p := snap.Processes[0]
	if p.State != supervisor.StateStarting || p.DetailCode != "storage_waiting" || p.DetailParams["path"] != data {
		t.Errorf("process %+v", p)
	}
	select {
	case err := <-done:
		t.Fatalf("the gate did not wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(data); err == nil {
		t.Fatal("the gate made the missing data folder")
	}
	// the disk comes back with its marker
	rec, _ := config.ReadStorageRecord(filepath.Dir(cfgPath))
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, config.StorageMarkerFile), []byte(rec.ID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the gate did not see the disk come back")
	}
	if _, err := processesAt(admin); err == nil {
		t.Error("the holding admin still answers after the gate")
	}
	// a single role does not wait: it says so
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	if _, err := storageGate(cfg, "api", io.Discard); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("a single role: %v", err)
	}
	// and the bundled Postgres makes no cluster there
	if _, err := bundledPostgres(t.Context(), &cfg, true); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("bundledPostgres: %v", err)
	}
}

// A move asked for from the web is done at the next start, before anything runs.
func TestServeDoesTheRequestedMove(t *testing.T) {
	cfgPath, data, _ := storageEnv(t)
	if err := os.MkdirAll(filepath.Join(data, "postgres"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "postgres", "PG_VERSION"), []byte("17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(filepath.Dir(data)), "Bigger", "Maktaba")
	if err := storage.WriteRequest(filepath.Dir(cfgPath), storage.Request{Target: target, By: "admin", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	next, err := storageGate(cfg, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if next.Server.DataDir != target {
		t.Errorf("data_dir %q after the move", next.Server.DataDir)
	}
	if _, err := os.Stat(filepath.Join(target, "postgres", "PG_VERSION")); err != nil {
		t.Error(err)
	}
	if _, ok := storage.ReadRequest(filepath.Dir(cfgPath)); ok {
		t.Error("the request is still there")
	}
	if b, _ := os.ReadFile(cfgPath); !strings.Contains(string(b), "# mine") {
		t.Errorf("server.toml lost its comments:\n%s", b)
	}
	// a request that cannot be done is recorded, and the install starts where it was
	if err := storage.WriteRequest(filepath.Dir(cfgPath), storage.Request{Target: "relative/dir"}); err != nil {
		t.Fatal(err)
	}
	again, err := storageGate(next, "", io.Discard)
	if err != nil || again.Server.DataDir != target {
		t.Fatalf("%v %q", err, again.Server.DataDir)
	}
	if r, ok := storage.ReadResult(filepath.Dir(cfgPath)); !ok || r.OK || r.Target != "relative/dir" {
		t.Errorf("result %+v", r)
	}
}

// /config/storage: the token is required; check answers the plan; move refuses a
// bad target (422) and asks for the restart that does a good one (202).
func TestConfigAdminStorageRoutes(t *testing.T) {
	cfgPath, data, _ := storageEnv(t)
	reloads := 0
	c := &configAdmin{path: cfgPath, token: "tok", reload: func() { reloads++ }}
	h := c.adminHandler(http.NotFoundHandler())
	call := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("GET", "/config/storage", "", ""); rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}
	rec := call("GET", "/config/storage", "", "tok")
	var doc storage.Doc
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.DataDir != data || !doc.CanMove {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	if rec := call("GET", "/config/storage/disks", "", "tok"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"disks"`) {
		t.Errorf("disks %d %s", rec.Code, rec.Body)
	}
	rec = call("POST", "/config/storage/check", `{"target":"rel"}`, "tok")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "target_relative") {
		t.Errorf("check %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/config/storage/move", `{"target":"rel"}`, "tok"); rec.Code != http.StatusUnprocessableEntity || reloads != 0 {
		t.Errorf("bad move %d (reloads %d)", rec.Code, reloads)
	}
	target := filepath.Join(filepath.Dir(filepath.Dir(data)), "Other")
	rec = call("POST", "/config/storage/move", `{"target":"`+target+`","by":"admin"}`, "tok")
	if rec.Code != http.StatusAccepted || reloads != 1 {
		t.Fatalf("move %d %s (reloads %d)", rec.Code, rec.Body, reloads)
	}
	if r, ok := storage.ReadRequest(filepath.Dir(cfgPath)); !ok || r.Target != target || r.By != "admin" {
		t.Errorf("request %+v", r)
	}
	rec = call("GET", "/config/storage", "", "tok")
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.Move.State != "requested" {
		t.Errorf("move state %+v", doc.Move)
	}
}
