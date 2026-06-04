package transcripts

import (
	"errors"
	"testing"
)

// fakeRows replays a fixed set of segment column-tuples through the
// rowScanner seam, optionally surfacing an iteration error.
type fakeRows struct {
	data [][]any
	pos  int
	err  error
}

func (r *fakeRows) Next() bool {
	if r.err != nil {
		return false
	}
	if r.pos >= len(r.data) {
		return false
	}
	r.pos++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.pos-1]
	if len(dest) != len(row) {
		return errors.New("column count mismatch")
	}
	*dest[0].(*int) = row[0].(int)
	*dest[1].(*float64) = row[1].(float64)
	*dest[2].(*float64) = row[2].(float64)
	*dest[3].(*string) = row[3].(string)
	*dest[4].(*string) = row[4].(string)
	return nil
}

func (r *fakeRows) Err() error { return r.err }

func TestMapSegments(t *testing.T) {
	rows := &fakeRows{data: [][]any{
		{0, 0.0, 1.5, "", "Bismillah"},
		{1, 1.5, 3.0, "Sheikh", "as-salamu alaykum"},
	}}
	segs, err := mapSegments(rows)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	// seq flows straight into Index (the VTT cue identifier).
	if segs[0].Index != 0 || segs[1].Index != 1 {
		t.Fatalf("seq not carried into Index: %+v", segs)
	}
	if segs[1].Speaker != "Sheikh" || segs[1].Text != "as-salamu alaykum" {
		t.Fatalf("speaker/text not scanned: %+v", segs[1])
	}
	if segs[0].StartSec != 0.0 || segs[0].EndSec != 1.5 {
		t.Fatalf("timings not scanned: %+v", segs[0])
	}
}

func TestMapSegments_Empty(t *testing.T) {
	segs, err := mapSegments(&fakeRows{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(segs) != 0 {
		t.Fatalf("expected no segments, got %d", len(segs))
	}
}

func TestMapSegments_IterationError(t *testing.T) {
	boom := errors.New("connection lost mid-scan")
	_, err := mapSegments(&fakeRows{err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped iteration error, got %v", err)
	}
}
