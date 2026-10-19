package system

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Epic 35 story 35.2: on a self-managed install the self-update stages the
// verified release in bin/releases/<v>/, makes it current under the
// launcher's guard and asks for a restart; the running binary is untouched.
func TestSelfUpdateLayoutStagesRelease(t *testing.T) {
	rig := newRig(t, defaultRepo)
	l := layout.New(t.TempDir())
	running := filepath.Join(l.ReleaseDir("1.0.0"), layout.Exe("maktaba-api"))
	if err := os.MkdirAll(filepath.Dir(running), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.ReleaseDir("1.0.0"), layout.Exe("maktaba-server")), []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.WriteVerified(l.ReleaseDir("1.0.0"), layout.IdentityMigrated, "1.0.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := l.WritePointer(layout.CurrentFile, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	rig.su.selfPathFunc = func() (string, error) { return running, nil }

	rec := rig.post(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["layout"] != true || body["rollback_to"] != "1.0.0" {
		t.Fatalf("body = %v", body)
	}
	if cur, _ := l.Current(); cur != "1.5.0" {
		t.Fatalf("current = %q", cur)
	}
	if p, _ := l.ReadPending(); p == nil || p.Version != "1.5.0" {
		t.Fatalf("pending = %+v", p)
	}
	if err := l.ValidateRelease("1.5.0"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(running); string(b) != "OLD" {
		t.Fatal("the running binary was replaced")
	}
	if _, err := os.Stat(l.RestartRequestPath()); err != nil {
		t.Fatal("no restart request for the launcher")
	}
	if l.Watermark() != "1.5.0" {
		t.Fatalf("watermark = %q", l.Watermark())
	}
	select {
	case p := <-rig.exec:
		t.Fatalf("re-exec'd %s on a layout install", p)
	default:
	}
}
