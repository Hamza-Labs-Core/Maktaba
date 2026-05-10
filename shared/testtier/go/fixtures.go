package testtier

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Story 20.2 (fixtures + seed data).
//
// Fixtures sit outside the tiers because they're consumed across
// tiers: a unit test may want a single canonical Library row, an
// integration test wants the full 1k-video seed.
//
// The fixtures here are *small builders*. They do not perform schema
// migration — that's the responsibility of the integration harness
// itself (it boots a goose-migrated database). Each builder returns
// the inserted ID so the test can chain.

// Library is the canonical fixture shape for a libraries row.
type Library struct {
	ID        string
	Name      string
	Root      string
	CreatedAt time.Time
}

// Video is the canonical fixture shape for a videos row.
type Video struct {
	ID          string
	LibraryID   string
	Path        string
	ContentHash string
	DurationSec float64
	State       string
}

// NewID returns a deterministic-ish UUID-shaped identifier. Tests use
// it to keep assertions readable. The seed is the test name so reruns
// are stable.
func NewID(t testing.TB, kind string) string {
	t.Helper()
	r := rand.New(rand.NewSource(int64(hashStr(t.Name() + "/" + kind))))
	var b [16]byte
	_, _ = r.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func hashStr(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * 16777619
	}
	return h
}

// MakeLibrary inserts a libraries row and returns the inserted ID.
// The caller is responsible for cleanup; integration tests usually run
// inside a transaction that rolls back.
func MakeLibrary(t testing.TB, db *sql.DB, lib Library) string {
	t.Helper()
	if lib.ID == "" {
		lib.ID = NewID(t, "library")
	}
	if lib.Name == "" {
		lib.Name = "Test Library"
	}
	if lib.Root == "" {
		lib.Root = t.TempDir()
	}
	if lib.CreatedAt.IsZero() {
		lib.CreatedAt = time.Now().UTC()
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO libraries (id, name, root, created_at)
		VALUES ($1, $2, $3, $4)
	`, lib.ID, lib.Name, lib.Root, lib.CreatedAt)
	if err != nil {
		t.Fatalf("MakeLibrary: %v", err)
	}
	return lib.ID
}

// MakeVideo inserts a videos row.
func MakeVideo(t testing.TB, db *sql.DB, v Video) string {
	t.Helper()
	if v.ID == "" {
		v.ID = NewID(t, "video")
	}
	if v.State == "" {
		v.State = "discovered"
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO videos (id, library_id, path, content_hash, duration_sec, state)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, v.ID, v.LibraryID, v.Path, v.ContentHash, v.DurationSec, v.State)
	if err != nil {
		t.Fatalf("MakeVideo: %v", err)
	}
	return v.ID
}

// SkipIfShort skips the test when -short is set; integration / e2e
// tests opt in by calling this at the top.
func SkipIfShort(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
}

// SkipIfNoDB skips the test when MAKTABA_TEST_DSN is unset. The
// integration harness wires the DSN; a developer running `go test`
// without compose gets skipped instead of a network error.
func SkipIfNoDB(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("MAKTABA_TEST_DSN")
	if dsn == "" {
		t.Skip("MAKTABA_TEST_DSN not set; integration tests skipped")
	}
	return dsn
}

// MustEnv reads an env var or fails the test. For values the test
// requires to even be meaningful (e.g. an OAuth client id).
func MustEnv(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("required env %s not set", key)
	}
	return v
}

// FixturesDir returns the path to the repo-level fixtures directory.
// Walks up from the test file looking for a sibling `tests/fixtures/`.
func FixturesDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("FixturesDir: runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 16; i++ {
		cand := filepath.Join(dir, "tests", "fixtures")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	t.Fatal("FixturesDir: tests/fixtures not found")
	return ""
}

// SafeReader is a tiny convenience around os.ReadFile that fails the
// test on error and rejects paths escaping FixturesDir (gosec-friendly).
func SafeReadFixture(t testing.TB, name string) []byte {
	t.Helper()
	dir := FixturesDir(t)
	full := filepath.Join(dir, name)
	cleaned, err := filepath.Abs(full)
	if err != nil {
		t.Fatal(err)
	}
	cleanedDir, _ := filepath.Abs(dir)
	if !strings.HasPrefix(cleaned, cleanedDir) {
		t.Fatalf("path %s escapes fixtures dir", name)
	}
	b, err := os.ReadFile(cleaned)
	if err != nil {
		t.Fatalf("SafeReadFixture: %v", err)
	}
	return b
}

// ErrFixtureMissing helps tests communicate a missing fixture in a way
// the runner can distinguish from real assertion failures.
var ErrFixtureMissing = errors.New("fixture missing")
