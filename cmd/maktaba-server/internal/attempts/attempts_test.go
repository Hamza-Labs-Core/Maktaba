package attempts

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The spec's example reads back as written, both files are merged newest first, the
// server's file keeps Keep, and the same id is replaced in place.
func TestAttemptsFiles(t *testing.T) {
	dir := t.TempDir()
	if got := Read(dir); len(got) != 0 {
		t.Fatalf("no files: %+v", got)
	}
	app := `{"attempts": [{"id": "app-1792735800123", "at": "2026-10-24T05:30:00Z",
	  "ended_at": "2026-10-24T05:31:12Z", "source": "app", "trigger": "badge", "from": "0.3.0-rc.5",
	  "to": "0.3.0", "outcome": "failed", "step": "install", "code": "needs_admin", "detail": "denied"}]}`
	if err := os.WriteFile(filepath.Join(dir, AppFile), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 24, 5, 0, 0, 0, time.UTC)
	if err := Record(dir, Attempt{ID: "start-0.3.0-1", At: base.Add(time.Hour), Source: SourceServer, To: "0.3.0",
		Outcome: Running, Step: "migrate"}); err != nil {
		t.Fatal(err)
	}
	got := Read(dir)
	if len(got) != 2 || got[0].ID != "start-0.3.0-1" || got[1].Code != "needs_admin" || got[1].EndedAt == nil ||
		got[1].From != "0.3.0-rc.5" {
		t.Fatalf("merged %+v", got)
	}
	// the same id is replaced; Find is the newest at a release
	a, ok := Find(dir, "v0.3.0")
	if !ok || a.ID != "start-0.3.0-1" {
		t.Fatalf("find %+v %v", a, ok)
	}
	a.Ended(PutBack)
	if err := Record(dir, a); err != nil {
		t.Fatal(err)
	}
	if got := Read(dir); len(got) != 2 || got[0].Outcome != PutBack || got[0].EndedAt == nil {
		t.Fatalf("replaced %+v", got)
	}
	// at most Keep in the server's file, at most Show in the status
	for i := 0; i < Keep+5; i++ {
		_ = Record(dir, Attempt{ID: "start-x-" + strconv.Itoa(i), At: base.Add(time.Duration(i+2) * time.Hour),
			Source: SourceServer, To: "9.9." + strconv.Itoa(i), Outcome: Failed})
	}
	if n := len(readFile(filepath.Join(dir, ServerFile))); n != Keep {
		t.Fatalf("kept %d", n)
	}
	if n := len(Read(dir)); n != Show {
		t.Fatalf("shown %d", n)
	}
	if Stamp(dir) == "" || Stamp(t.TempDir()) != "" {
		t.Fatal("stamp")
	}
	// an entry this build cannot read costs that entry only
	bad := t.TempDir()
	mixed := `{"attempts": [{"id": "a", "at": "not a time", "source": "app", "to": "1", "outcome": "failed"},
	  {"id": "b", "at": "2026-10-24T05:30:00Z", "source": "app", "to": "", "outcome": "failed", "step": "check"}]}`
	_ = os.WriteFile(filepath.Join(bad, AppFile), []byte(mixed), 0o644)
	if got := Read(bad); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("mixed %+v", got)
	}
}
