package probe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeRow scans a fixed column slice (or returns a fixed error) into
// the destination pointers, mirroring how pgx.Row.Scan assigns by
// position. Only the column types lookupSQL actually selects are
// handled.
type fakeRow struct {
	cols []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.cols) {
		return errors.New("fakeRow: column count mismatch")
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *uuid.UUID:
			*p = r.cols[i].(uuid.UUID)
		case *string:
			*p = r.cols[i].(string)
		case *int:
			*p = r.cols[i].(int)
		case *int64:
			*p = r.cols[i].(int64)
		case *float64:
			*p = r.cols[i].(float64)
		case *time.Time:
			*p = r.cols[i].(time.Time)
		case *bool:
			*p = r.cols[i].(bool)
		default:
			return errors.New("fakeRow: unsupported dest type")
		}
	}
	return nil
}

type fakeQuerier struct {
	row     fakeRow
	gotSQL  string
	gotArgs []any
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.gotSQL = sql
	q.gotArgs = args
	return q.row
}

func okCols(vid, lib uuid.UUID, probed bool) []any {
	return []any{
		vid,                            // id
		lib,                            // library_id
		"abc123",                       // content_hash
		"/media/movie.mkv",             // path
		"mkv",                          // container
		"h264",                         // video_codec
		"aac",                          // audio_codec
		1080,                           // height
		1920,                           // width
		3600.5,                         // duration_sec
		8000,                           // bitrate_kbps
		2,                              // audio_channels
		int64(1024),                    // size_bytes
		time.Unix(1700000000, 0).UTC(), // mtime
		probed,                         // probed
	}
}

func TestPGBackend_LookupSuccess(t *testing.T) {
	vid, lib := uuid.New(), uuid.New()
	q := &fakeQuerier{row: fakeRow{cols: okCols(vid, lib, true)}}

	row, err := lookupOn(context.Background(), q, vid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.VideoID != vid || row.LibraryID != lib {
		t.Fatalf("ids not scanned: %+v", row)
	}
	if row.Container != "mkv" || row.VideoCodec != "h264" || row.AudioCodec != "aac" {
		t.Fatalf("codecs not scanned: %+v", row)
	}
	if row.Height != 1080 || row.Width != 1920 || row.AudioChannels != 2 {
		t.Fatalf("dimensions not scanned: %+v", row)
	}
	if row.DurationSec != 3600.5 || row.BitrateKbps != 8000 || row.Size != 1024 {
		t.Fatalf("metrics not scanned: %+v", row)
	}
	if !row.Probed {
		t.Fatalf("expected probed=true")
	}
	// The single bound arg must be the video id.
	if len(q.gotArgs) != 1 || q.gotArgs[0] != vid {
		t.Fatalf("expected video id bound as $1, got %v", q.gotArgs)
	}
}

func TestPGBackend_NotProbed(t *testing.T) {
	vid, lib := uuid.New(), uuid.New()
	q := &fakeQuerier{row: fakeRow{cols: okCols(vid, lib, false)}}

	_, err := lookupOn(context.Background(), q, vid)
	if !errors.Is(err, ErrNotProbed) {
		t.Fatalf("expected ErrNotProbed, got %v", err)
	}
}

func TestPGBackend_NotFound(t *testing.T) {
	vid := uuid.New()
	q := &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}

	_, err := lookupOn(context.Background(), q, vid)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPGBackend_ScanErrorWrapped(t *testing.T) {
	vid := uuid.New()
	sentinel := errors.New("connection reset")
	q := &fakeQuerier{row: fakeRow{err: sentinel}}

	_, err := lookupOn(context.Background(), q, vid)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotProbed) {
		t.Fatalf("a real DB error must not masquerade as not-found/not-probed: %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel, got %v", err)
	}
}
