package attempts

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// A detail keeps no URL's query (a signed download's signature) and at most MaxDetail
// bytes, on write and on read; changes from two goroutines both land.
func TestCleanAndConcurrentRecords(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("é", 3000)
	if err := Record(dir, Attempt{ID: "a", At: time.Now(), To: "0.3.1", Outcome: Failed,
		Detail: `GET https://objects.githubusercontent.com/x/y.tar.gz?X-Amz-Signature=abc&sig=def: 403 ` + long}); err != nil {
		t.Fatal(err)
	}
	got := Read(dir)[0].Detail
	if strings.Contains(got, "Signature") || strings.Contains(got, "sig=") || !strings.Contains(got, "https://objects.githubusercontent.com/x/y.tar.gz: 403") ||
		len(got) > MaxDetail || !strings.HasSuffix(got, "…") {
		t.Fatalf("the detail: %d %q", len(got), got[:120])
	}
	// a file the app wrote with a long detail is cut when read
	app := `{"attempts":[{"id":"app-1","at":"2026-10-24T05:30:00Z","source":"app","to":"0.3.1","outcome":"failed","detail":"` +
		strings.Repeat("x", 5000) + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, AppFile), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range Read(dir) {
		if len(a.Detail) > MaxDetail {
			t.Fatalf("read uncut: %s %d", a.ID, len(a.Detail))
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = Record(dir, Attempt{ID: "c" + strconv.Itoa(i), At: time.Now(), To: "0.3.1", Outcome: Running})
		}(i)
	}
	wg.Wait()
	if n := len(readFile(filepath.Join(dir, ServerFile))); n != 9 {
		t.Fatalf("concurrent records: %d of 9", n)
	}
	// the running ones at another release than the one that runs now end failed
	if n, err := SettleStale(dir, "v0.3.2"); err != nil || n != 8 {
		t.Fatalf("settled %d %v", n, err)
	}
	if n, _ := SettleStale(dir, "0.3.2"); n != 0 {
		t.Fatalf("settled twice: %d", n)
	}
}

// A URL's query goes, the punctuation around it stays.
func TestCleanKeepsPunctuation(t *testing.T) {
	for in, want := range map[string]string{
		"GET https://x/a.tar.gz?sig=s3cret): 403":            "GET https://x/a.tar.gz): 403",
		"(see https://x/p?token=t.)":                         "(see https://x/p.)",
		"fetching https://x/a?X-Amz-Signature=abc&s=1, then": "fetching https://x/a, then",
		"https://x/plain: no query":                          "https://x/plain: no query",
	} {
		if got := Clean(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
