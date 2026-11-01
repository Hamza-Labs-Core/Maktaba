package probe

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		"smpte2084",                    // color_transfer
		"bt2020nc",                     // color_space
		"bt2020",                       // color_primaries
		"tv",                           // color_range
		"yuv420p10le",                  // pix_fmt
		true,                           // has_audio
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
	if row.ColorTransfer != "smpte2084" || row.ColorMatrix != "bt2020nc" || row.ColorPrimaries != "bt2020" ||
		row.ColorRange != "tv" || row.PixFmt != "yuv420p10le" {
		t.Fatalf("the video stream's colour not scanned: %+v", row)
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

// seqQuerier answers each lookup with the next row (the last repeats) and records Exec.
type seqQuerier struct {
	rows     []fakeRow
	n        int
	execSQL  []string
	affected int64
	execErr  error
}

func (q *seqQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	r := q.rows[min(q.n, len(q.rows)-1)]
	q.n++
	return r
}

func (q *seqQuerier) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	q.execSQL = append(q.execSQL, sql)
	if q.execErr != nil {
		return pgconn.CommandTag{}, q.execErr
	}
	if sql == bumpProbeSQL {
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", q.affected)), nil
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func TestProbeOnPlay_QueuesAndWaitsForTheRow(t *testing.T) {
	vid, lib := uuid.New(), uuid.New()
	q := &seqQuerier{rows: []fakeRow{
		{cols: okCols(vid, lib, false)},
		{cols: okCols(vid, lib, true)},
	}}
	row, err := probeOnPlay(context.Background(), q, vid, time.Second, time.Millisecond)
	if err != nil || row == nil || row.VideoCodec != "h264" {
		t.Fatalf("got %+v, %v", row, err)
	}
	if len(q.execSQL) != 2 || q.execSQL[1] != insertProbeSQL {
		t.Fatalf("no open job: bump, then insert; got %d statements", len(q.execSQL))
	}
}

func TestProbeOnPlay_BumpsAnOpenJobWithoutInserting(t *testing.T) {
	vid, lib := uuid.New(), uuid.New()
	q := &seqQuerier{affected: 1, rows: []fakeRow{{cols: okCols(vid, lib, true)}}}
	if _, err := probeOnPlay(context.Background(), q, vid, time.Second, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(q.execSQL) != 1 || q.execSQL[0] != bumpProbeSQL {
		t.Fatalf("an open job is moved to the front, not duplicated: %v", q.execSQL)
	}
}

func TestProbeOnPlay_GivesUpAfterTheWait(t *testing.T) {
	vid, lib := uuid.New(), uuid.New()
	q := &seqQuerier{rows: []fakeRow{{cols: okCols(vid, lib, false)}}}
	start := time.Now()
	_, err := probeOnPlay(context.Background(), q, vid, 30*time.Millisecond, time.Millisecond)
	if !errors.Is(err, ErrNotProbed) {
		t.Fatalf("err = %v, want ErrNotProbed", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("waited past the bound")
	}
}

func TestProbeOnPlay_QueueFailureIsNotProbed(t *testing.T) {
	q := &seqQuerier{execErr: errors.New("db down"), rows: []fakeRow{{}}}
	if _, err := probeOnPlay(context.Background(), q, uuid.New(), time.Second, time.Millisecond); !errors.Is(err, ErrNotProbed) {
		t.Fatalf("err = %v", err)
	}
	if q.n != 0 {
		t.Fatal("no wait when the probe could not be queued")
	}
}
