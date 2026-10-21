package probe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func snapRow(id uuid.UUID, path string) Row {
	return Row{VideoID: id, LibraryID: uuid.New(), Path: path, Container: "mp4", VideoCodec: "h264",
		AudioCodec: "aac", Height: 1080, Width: 1920, DurationSec: 14, Size: 1000,
		ModTime: time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC), Probed: true}
}

func TestSnapshotKeepsAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node", SnapshotFile)
	a, b := uuid.New(), uuid.New()
	s := OpenSnapshot(path, nil)
	if s.Len() != 0 {
		t.Fatalf("a new snapshot: %d rows", s.Len())
	}
	pm := map[string]string{"/Volumes/Originals/": "O:/"}
	rows := []Row{snapRow(a, "/Volumes/Originals/a.mp4"), snapRow(b, "/Volumes/Originals/b.mp4")}
	if err := s.Replace(rows, pm); err != nil {
		t.Fatal(err)
	}
	again := OpenSnapshot(path, nil) // a restart in an outage
	r, ok := again.Lookup(a)
	if !ok || r.Path != "/Volumes/Originals/a.mp4" || r.VideoCodec != "h264" || !r.Probed || !r.ModTime.Equal(snapRow(a, "").ModTime) {
		t.Fatalf("reopened row: %+v %v", r, ok)
	}
	if got := again.PathMap(); got["/Volumes/Originals/"] != "O:/" {
		t.Fatalf("reopened path map: %v", got)
	}
	before, _ := os.ReadFile(path)
	if err := s.Replace(rows, pm); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("an unchanged snapshot was rewritten")
	}
}

func TestSnapshotUnreadableFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), SnapshotFile)
	if err := os.WriteFile(path, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := OpenSnapshot(path, nil); s.Len() != 0 {
		t.Fatalf("an unreadable file: %d rows", s.Len())
	}
}

type failingSource struct{ err error }

func (f failingSource) AllRows(context.Context) ([]Row, error)             { return nil, f.err }
func (f failingSource) PathMap(context.Context) (map[string]string, error) { return nil, nil }

func TestSnapshotFailedRefreshKeepsRows(t *testing.T) {
	a := uuid.New()
	s := OpenSnapshot("", nil)
	_ = s.Replace([]Row{snapRow(a, "/x/a.mp4")}, nil)
	if err := s.Refresh(context.Background(), failingSource{err: errors.New("database gone")}); err == nil {
		t.Fatal("a failed read refreshed")
	}
	if _, ok := s.Lookup(a); !ok {
		t.Fatal("a failed refresh dropped the rows")
	}
}

// failingBackend is the home's database gone.
type failingBackend struct{ calls int }

func (f *failingBackend) Lookup(context.Context, uuid.UUID) (*Row, error) {
	f.calls++
	return nil, errors.New("probe: lookup video: dial tcp: connect: connection refused")
}

func TestOfflineBackend(t *testing.T) {
	a, missing := uuid.New(), uuid.New()
	snap := OpenSnapshot("", nil)
	_ = snap.Replace([]Row{snapRow(a, "/x/a.mp4")}, nil)

	// the database answers: its answer stands, no-such-video included
	fake := NewFakeBackend()
	live := snapRow(a, "/x/live.mp4")
	fake.Set(&live)
	o := &Offline{Primary: fake, Snap: snap}
	if r, err := o.Lookup(context.Background(), a); err != nil || r.Path != "/x/live.mp4" {
		t.Fatalf("the database answering: %+v %v", r, err)
	}
	if _, err := o.Lookup(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown video with the database answering: %v, want ErrNotFound", err)
	}

	// the database fails: the snapshot answers, the failure is recorded
	dead := &failingBackend{}
	var failed []error
	down := false
	o = &Offline{Primary: dead, Snap: snap, Down: func() bool { return down },
		Failed: func(err error) { failed = append(failed, err); down = true }}
	if r, err := o.Lookup(context.Background(), a); err != nil || r.Path != "/x/a.mp4" {
		t.Fatalf("the database failing: %+v %v", r, err)
	}
	if len(failed) != 1 {
		t.Fatalf("the failure was not recorded: %v", failed)
	}
	// known down: not asked at all
	if _, err := o.Lookup(context.Background(), missing); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a video outside the snapshot: %v, want ErrUnavailable", err)
	}
	if dead.calls != 1 {
		t.Fatalf("a database known down was asked: %d calls", dead.calls)
	}
}
