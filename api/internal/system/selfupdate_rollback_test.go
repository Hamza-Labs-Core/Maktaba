package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A swapped binary that cannot be executed is rolled back: the .bak is put
// back and the status reports rolled_back.
func TestRestartRollsBackWhenExecFails(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "maktaba-server")
	if err := os.WriteFile(self, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(self, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	su := NewSelfUpdater(NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", Disabled: true}))
	su.execSelf = func(string) error { return errors.New("exec format error") }
	su.restart(self, "v1.1.0")
	if b, _ := os.ReadFile(self); string(b) != "OLD" {
		t.Fatalf("binary after a failed exec = %q, want the old one back", b)
	}
	rb := su.LastRollback()
	if rb == nil || rb.Version != "v1.1.0" || rb.Reason != "exec format error" {
		t.Fatalf("LastRollback = %+v, want v1.1.0 / exec format error", rb)
	}
}

// Success, and a platform that relaunches through its supervisor, are not
// rollbacks.
func TestRestartKeepsBinaryOnSuccessOrSupervisorRelaunch(t *testing.T) {
	for name, execErr := range map[string]error{
		"exec ok":     nil,
		"unsupported": fmt.Errorf("%w; relaunch handled by the supervisor", errReexecUnsupported),
	} {
		dir := t.TempDir()
		self := filepath.Join(dir, "maktaba-server")
		_ = os.WriteFile(self, []byte("OLD"), 0o755)
		if err := swapBinary(self, []byte("NEW")); err != nil {
			t.Fatal(err)
		}
		su := NewSelfUpdater(NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", Disabled: true}))
		su.execSelf = func(string) error { return execErr }
		su.restart(self, "v1.1.0")
		if b, _ := os.ReadFile(self); string(b) != "NEW" {
			t.Errorf("%s: binary = %q, want NEW kept", name, b)
		}
		if su.LastRollback() != nil {
			t.Errorf("%s: reported a rollback", name)
		}
	}
}

func statusDoc(t *testing.T, su *SelfUpdater, base http.Handler) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	su.StatusHandler(base).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/updates", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func fixedStatus(doc string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	})
}

func TestStatusHandlerReportsDockerInstructionsUpFront(t *testing.T) {
	su := NewSelfUpdater(NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", Disabled: true}))
	su.detect = func(string) installKind { return installDocker }
	su.selfPathFunc = func() (string, error) { return "/usr/bin/maktaba-server", nil }
	doc := statusDoc(t, su, fixedStatus(`{"available":true,"latest_version":"v1.2.0","channel":"stable"}`))
	if doc["install"] != "docker" || doc["instructions"] != dockerInstructions {
		t.Fatalf("doc = %v, want install docker with the compose instructions", doc)
	}
	if doc["image"] != "ghcr.io/hamza-labs-core/maktaba-server:1.2.0" {
		t.Errorf("image = %v", doc["image"])
	}
	if doc["available"] != true || doc["channel"] != "stable" {
		t.Errorf("base fields lost: %v", doc)
	}
	if _, ok := doc["rolled_back"]; ok {
		t.Errorf("rolled_back present without a rollback")
	}
}

func TestStatusHandlerReportsBinaryAndRollback(t *testing.T) {
	su := NewSelfUpdater(NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", Disabled: true}))
	su.detect = func(string) installKind { return installBinary }
	su.selfPathFunc = func() (string, error) { return filepath.Join(t.TempDir(), "maktaba-server"), nil }
	su.rolledBack = &RollbackInfo{Version: "v1.1.0", Reason: "exec format error"}
	doc := statusDoc(t, su, fixedStatus(`{"available":true,"latest_version":"v1.1.0"}`))
	if doc["install"] != "binary" {
		t.Fatalf("install = %v, want binary", doc["install"])
	}
	if _, ok := doc["instructions"]; ok {
		t.Errorf("instructions on a binary install: %v", doc)
	}
	rb, _ := doc["rolled_back"].(map[string]any)
	if rb["version"] != "v1.1.0" {
		t.Errorf("rolled_back = %v", doc["rolled_back"])
	}
}

func TestStatusHandlerPassesErrorsThrough(t *testing.T) {
	su := NewSelfUpdater(NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", Disabled: true}))
	rec := httptest.NewRecorder()
	su.StatusHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("down"))
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "down" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}
