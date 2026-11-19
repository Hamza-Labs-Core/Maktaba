package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func chromaConfig(t *testing.T) (config.Config, string) {
	t.Helper()
	cfg := indexConfig(t)
	t.Setenv("MAKTABA_CHROMA_DIR", "")
	cfg.Database.URL = "postgres://maktaba@127.0.0.1:54329/maktaba"
	return cfg, filepath.Join(cfg.StateDir(), "chroma")
}

// copyMarker is the done marker the copy of the releases before 43.5 wrote (JSON: a
// Windows path's backslashes escaped).
func copyMarker(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"chroma_dir": dir, "done_at": 1})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func makeChroma(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "0b5e6f9c-1d2e-4f30-8a9b-0c1d2e3f4a5b"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"chroma.sqlite3", filepath.Join("0b5e6f9c-1d2e-4f30-8a9b-0c1d2e3f4a5b", "data_level0.bin")} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func waitFileGone(t *testing.T, path, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s is still there", what, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestACopiedChromaFolderIsDeletedAtTheStart: a home whose copy was checked (the done
// file of the release before names its folder) loses the folder at this start, and the
// copy's progress with it; the done file stays (a rollback's release reads it).
func TestACopiedChromaFolderIsDeletedAtTheStart(t *testing.T) {
	cfg, dir := chromaConfig(t)
	state := cfg.StateDir()
	makeChroma(t, dir)
	done := filepath.Join(state, ChromaCopyDone)
	progress := filepath.Join(state, ChromaCopyProgress)
	for path, body := range map[string][]byte{done: copyMarker(t, dir), progress: []byte(`{"phase":"done"}`)} {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var log syncBuffer
	if got := RetireChroma(cfg, &log); got != dir {
		t.Fatalf("RetireChroma = %q, want %q", got, dir)
	}
	waitFileGone(t, dir, "a copied folder")
	waitFileGone(t, progress, "the copy's progress")
	if !exists(done) {
		t.Error("the done file went: a rollback's release would copy again")
	}
	waitChromaLog(t, &log, "deleted "+dir)
}

// TestAChromaFolderNeverCopiedIsKeptADay: a folder whose copy never finished is kept at
// the first start of this release (a rollback finds it) and marked; a start within
// ChromaKeep of the mark keeps it still (an update may start the release more than once
// before its watch ends), a later one deletes it. A done file naming another folder does
// not count.
func TestAChromaFolderNeverCopiedIsKeptADay(t *testing.T) {
	t.Setenv("MAKTABA_CHROMA_KEEP_SECONDS", "")
	cfg, dir := chromaConfig(t)
	state := cfg.StateDir()
	makeChroma(t, dir)
	other := filepath.Join(t.TempDir(), "chroma")
	if err := os.WriteFile(filepath.Join(state, ChromaCopyDone), copyMarker(t, other), 0o644); err != nil {
		t.Fatal(err)
	}
	var log syncBuffer
	if got := RetireChroma(cfg, &log); got != "" {
		t.Fatalf("first start: RetireChroma = %q, want the folder kept", got)
	}
	time.Sleep(100 * time.Millisecond)
	if !exists(filepath.Join(dir, "chroma.sqlite3")) {
		t.Fatal("first start: the folder never copied was deleted")
	}
	marker := filepath.Join(state, ChromaRetired)
	if !exists(marker) {
		t.Fatal("first start: no retirement mark")
	}
	if !strings.Contains(log.String(), "kept for this start") {
		t.Errorf("first start's log: %q", log.String())
	}
	if got := RetireChroma(cfg, &log); got != "" {
		t.Fatalf("a start within a day of the mark: RetireChroma = %q, want the folder kept", got)
	}
	if !exists(filepath.Join(dir, "chroma.sqlite3")) || !strings.Contains(log.String(), "kept since") {
		t.Fatalf("a start within a day of the mark: %q", log.String())
	}
	// a day later
	old, _ := json.Marshal(map[string]any{"chroma_dir": dir, "kept_at": time.Now().Add(-ChromaKeep - time.Minute).UTC().Format(time.RFC3339)})
	if err := os.WriteFile(marker, old, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := RetireChroma(cfg, &log); got != dir {
		t.Fatalf("second start: RetireChroma = %q, want %q", got, dir)
	}
	waitFileGone(t, dir, "the second start")
	waitFileGone(t, marker, "the retirement mark")
	// a later start: nothing to do, nothing fails
	if got := RetireChroma(cfg, &log); got != "" {
		t.Fatalf("third start: RetireChroma = %q", got)
	}
}

// TestRetireChromaLeavesOtherFolders: a MAKTABA_CHROMA_DIR that is not a Chroma folder
// (no chroma.sqlite3, not empty) is never deleted, whatever the marks say.
func TestRetireChromaLeavesOtherFolders(t *testing.T) {
	cfg, _ := chromaConfig(t)
	dir := filepath.Join(t.TempDir(), "mine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_CHROMA_DIR", dir)
	if err := os.WriteFile(filepath.Join(cfg.StateDir(), ChromaCopyDone), copyMarker(t, dir), 0o644); err != nil {
		t.Fatal(err)
	}
	var log syncBuffer
	for i := 0; i < 2; i++ {
		if got := RetireChroma(cfg, &log); got != "" {
			t.Fatalf("start %d: RetireChroma = %q", i, got)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if !exists(filepath.Join(dir, "notes.txt")) {
		t.Fatal("a folder that is not Chroma's was deleted")
	}
}

// TestRetireChromaWithNothingThere: no folder, no marks: nothing happens, and stale marks
// of an earlier start go.
func TestRetireChromaWithNothingThere(t *testing.T) {
	cfg, _ := chromaConfig(t)
	marker := filepath.Join(cfg.StateDir(), ChromaRetired)
	if err := os.WriteFile(marker, []byte(`{"chroma_dir":"/gone"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var log syncBuffer
	if got := RetireChroma(cfg, &log); got != "" {
		t.Fatalf("RetireChroma = %q", got)
	}
	if exists(marker) {
		t.Error("a stale retirement mark stays")
	}
	if log.String() != "" {
		t.Errorf("log: %q", log.String())
	}
}

func waitChromaLog(t *testing.T, log *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(log.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log %q lacks %q", log.String(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTheKeepIsSetByTheEnvironment: MAKTABA_CHROMA_KEEP_SECONDS (the rehearsal's) makes the
// wait shorter.
func TestTheKeepIsSetByTheEnvironment(t *testing.T) {
	cfg, dir := chromaConfig(t)
	t.Setenv("MAKTABA_CHROMA_KEEP_SECONDS", "0")
	makeChroma(t, dir)
	var log syncBuffer
	if got := RetireChroma(cfg, &log); got != "" {
		t.Fatalf("the first start deletes nothing, whatever the keep: %q", got)
	}
	if got := RetireChroma(cfg, &log); got != dir {
		t.Fatalf("a keep of 0: the second start deletes, got %q", got)
	}
	waitFileGone(t, dir, "a keep of 0")
	if chromaKeepFor("junk") != ChromaKeep || chromaKeepFor("-1") != ChromaKeep || chromaKeepFor("") != ChromaKeep {
		t.Error("a keep that is not a number of seconds is the default")
	}
}

func chromaKeepFor(v string) time.Duration {
	prev, had := os.LookupEnv("MAKTABA_CHROMA_KEEP_SECONDS")
	_ = os.Setenv("MAKTABA_CHROMA_KEEP_SECONDS", v)
	defer func() {
		if had {
			_ = os.Setenv("MAKTABA_CHROMA_KEEP_SECONDS", prev)
		} else {
			_ = os.Unsetenv("MAKTABA_CHROMA_KEEP_SECONDS")
		}
	}()
	return chromaKeep()
}

// TestOnlyChromasOwnEntriesAreDeleted: a MAKTABA_CHROMA_DIR an operator pointed at a
// folder that holds other things too (the state dir itself, say) loses only Chroma's
// database and segment folders; the folder and the rest stay.
func TestOnlyChromasOwnEntriesAreDeleted(t *testing.T) {
	cfg, _ := chromaConfig(t)
	dir := cfg.StateDir()
	t.Setenv("MAKTABA_CHROMA_DIR", dir)
	makeChroma(t, dir)
	for _, f := range []string{"chroma.sqlite3-wal", "chroma.sqlite3-shm"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keep := map[string]string{"postgres": "PG_VERSION", "backups": "pre.dump", "models": "m.bin"}
	for d, f := range keep {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, d, f), []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ChromaCopyDone), copyMarker(t, dir), 0o644); err != nil {
		t.Fatal(err)
	}
	var log syncBuffer
	if got := RetireChroma(cfg, &log); got != dir {
		t.Fatalf("RetireChroma = %q", got)
	}
	waitFileGone(t, filepath.Join(dir, "chroma.sqlite3"), "Chroma's database")
	waitChromaLog(t, &log, "deleted "+dir)
	for _, gone := range []string{"chroma.sqlite3-wal", "chroma.sqlite3-shm", "0b5e6f9c-1d2e-4f30-8a9b-0c1d2e3f4a5b"} {
		if exists(filepath.Join(dir, gone)) {
			t.Errorf("%s (Chroma's) is still there", gone)
		}
	}
	for d, f := range keep {
		if !exists(filepath.Join(dir, d, f)) {
			t.Errorf("%s/%s, not Chroma's, was deleted", d, f)
		}
	}
	if !strings.Contains(log.String(), "not Chroma's: they are kept") {
		t.Errorf("log: %q", log.String())
	}
}

// TestAChromaDirUnderHomeIsExpanded: MAKTABA_CHROMA_DIR=~/x is the home folder's x, as
// the pipeline read it.
func TestAChromaDirUnderHomeIsExpanded(t *testing.T) {
	cfg, _ := chromaConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MAKTABA_CHROMA_DIR", "~/vec")
	if got, want := ChromaDir(cfg), filepath.Join(home, "vec"); got != want {
		t.Fatalf("ChromaDir = %q, want %q", got, want)
	}
}
