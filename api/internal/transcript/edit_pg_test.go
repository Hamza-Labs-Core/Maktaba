package transcript_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript"
	tt "github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript/transcripttest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript/worddata"
)

func inTx(t *testing.T, db *sql.DB, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func text(t *testing.T, db *sql.DB, w tt.World, seq int) (string, []byte) {
	t.Helper()
	var s string
	var d []byte
	if err := db.QueryRow(`SELECT text, word_data FROM transcript_segments WHERE transcript_id = $1 AND seq = $2`,
		w.TranscriptID, seq).Scan(&s, &d); err != nil {
		t.Fatal(err)
	}
	return s, d
}

// A correction replaces the word, shifts the later words and flags, bumps the revision,
// records the edit and queues the subtitle files and one sentence's vector; its undo
// gives everything back.
func TestApplyEditAndUndo(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	w := tt.Seed(t, db, tt.Sentence{Text: "قال الصلوة خير من النوم", WithWords: true})
	ctx := context.Background()
	flag := tt.Flag(t, db, w, 0, 4, 6, "الصلوة", "الصلوه", "ar", 0.7)
	later := tt.Flag(t, db, w, 0, 15, 2, "من", "من", "ar", 0.6)
	before, beforeData := text(t, db, w, 0)
	var res transcript.Result
	if err := inTx(t, db, func(tx *sql.Tx) error {
		var err error
		res, err = transcript.ApplyEdit(ctx, tx, transcript.Edit{TranscriptID: w.TranscriptID, SegmentSeq: 0,
			CharStart: 4, CharLen: 6, Before: "الصلوة", After: "الصلاة!", FlagID: flag, UserID: w.AdminID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, data := text(t, db, w, 0)
	if got != "قال الصلاة! خير من النوم" {
		t.Fatalf("text %q", got)
	}
	slots, _ := worddata.Decode(data)
	if slots[1].CharLen != 7 || slots[2].CharStart != 12 || slots[1].Prob != 255 {
		t.Fatalf("word data %+v", slots)
	}
	var rev, cs int
	_ = db.QueryRow(`SELECT revision FROM transcripts WHERE id = $1`, w.TranscriptID).Scan(&rev)
	_ = db.QueryRow(`SELECT char_start FROM word_flags WHERE id = $1`, later).Scan(&cs)
	if rev != 1 || cs != 16 {
		t.Fatalf("revision %d, later flag at %d", rev, cs)
	}
	var jobs int
	_ = db.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE subject_id = $1 AND stage IN ('subtitle_gen', 'index')`, w.VideoID).Scan(&jobs)
	if jobs != 2 {
		t.Fatalf("%d follow-up jobs", jobs)
	}
	var payload string
	_ = db.QueryRow(`SELECT payload::text FROM processing_jobs WHERE subject_id = $1 AND stage = 'index'`, w.VideoID).Scan(&payload)
	if payload == "" || !contains(payload, `"seqs": [0]`) {
		t.Fatalf("index payload %s", payload)
	}
	if err := inTx(t, db, func(tx *sql.Tx) error { return transcript.Undo(ctx, tx, res.EditID) }); err != nil {
		t.Fatal(err)
	}
	back, backData := text(t, db, w, 0)
	if back != before {
		t.Fatalf("undone text %q", back)
	}
	bs, _ := worddata.Decode(backData)
	ws, _ := worddata.Decode(beforeData)
	if len(bs) != len(ws) || bs[2].CharStart != ws[2].CharStart {
		t.Fatalf("undone word data %+v vs %+v", bs, ws)
	}
	var status string
	var undone *time.Time
	_ = db.QueryRow(`SELECT status FROM word_flags WHERE id = $1`, flag).Scan(&status)
	_ = db.QueryRow(`SELECT undone_at FROM transcript_edits WHERE id = $1`, res.EditID).Scan(&undone)
	if status != "open" || undone == nil {
		t.Fatalf("after undo: flag %s, undone_at %v", status, undone)
	}
	if err := inTx(t, db, func(tx *sql.Tx) error { return transcript.Undo(ctx, tx, res.EditID) }); !errors.Is(err, transcript.ErrUndone) {
		t.Fatalf("second undo: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// The sentence changed since the flag: 409 stale, the flag marked; an undo over a later
// edit: changed_since; a superseded transcript: not_active.
func TestStaleChangedSinceAndNotActive(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	w := tt.Seed(t, db, tt.Sentence{Text: "a thw b", WithWords: true})
	flag := tt.Flag(t, db, w, 0, 2, 3, "thw", "thw", "en", 0.7)
	err := inTx(t, db, func(tx *sql.Tx) error {
		_, err := transcript.ApplyEdit(ctx, tx, transcript.Edit{TranscriptID: w.TranscriptID, CharStart: 2, CharLen: 3,
			Before: "tah", After: "the", FlagID: flag})
		return err
	})
	if !errors.Is(err, transcript.ErrStale) {
		t.Fatalf("stale: %v", err)
	}
	var first, second transcript.Result
	_ = inTx(t, db, func(tx *sql.Tx) error {
		first, err = transcript.ApplyEdit(ctx, tx, transcript.Edit{TranscriptID: w.TranscriptID, CharStart: 2, CharLen: 3, Before: "thw", After: "the"})
		return err
	})
	_ = inTx(t, db, func(tx *sql.Tx) error {
		second, err = transcript.ApplyEdit(ctx, tx, transcript.Edit{TranscriptID: w.TranscriptID, CharStart: 2, CharLen: 3, Before: "the", After: "thee"})
		return err
	})
	if first.EditID == "" || second.EditID == "" {
		t.Fatal("edits failed")
	}
	if err := inTx(t, db, func(tx *sql.Tx) error { return transcript.Undo(ctx, tx, first.EditID) }); !errors.Is(err, transcript.ErrChangedSince) {
		t.Fatalf("undo under a later edit: %v", err)
	}
	if got, _ := text(t, db, w, 0); got != "a thee b" {
		t.Fatalf("nothing should change: %q", got)
	}
	if _, err := db.Exec(`UPDATE transcripts SET is_active = false WHERE id = $1`, w.TranscriptID); err != nil {
		t.Fatal(err)
	}
	err = inTx(t, db, func(tx *sql.Tx) error {
		_, err := transcript.ApplyEdit(ctx, tx, transcript.Edit{TranscriptID: w.TranscriptID, CharStart: 0, CharLen: 1, Before: "a", After: "A"})
		return err
	})
	if !errors.Is(err, transcript.ErrNotActive) {
		t.Fatalf("not active: %v", err)
	}
}

// A batch undoes all or nothing, and names the edit that could not be undone.
func TestBatchUndoIsAllOrNothing(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	w := tt.Seed(t, db, tt.Sentence{Text: "x thw y", WithWords: true}, tt.Sentence{Text: "thw z", Start: 5, WithWords: true})
	batch := "33333333-3333-4333-8333-333333333333"
	for _, e := range []transcript.Edit{
		{TranscriptID: w.TranscriptID, SegmentSeq: 0, CharStart: 2, CharLen: 3, Before: "thw", After: "the", BatchID: batch},
		{TranscriptID: w.TranscriptID, SegmentSeq: 1, CharStart: 0, CharLen: 3, Before: "thw", After: "the", BatchID: batch},
	} {
		if err := inTx(t, db, func(tx *sql.Tx) error { _, err := transcript.ApplyEdit(ctx, tx, e); return err }); err != nil {
			t.Fatal(err)
		}
	}
	// someone edits the second sentence's word again: the batch cannot be undone whole
	if err := inTx(t, db, func(tx *sql.Tx) error {
		_, err := transcript.ApplyEdit(ctx, tx, transcript.Edit{TranscriptID: w.TranscriptID, SegmentSeq: 1, CharStart: 0, CharLen: 3, Before: "the", After: "The"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := inTx(t, db, func(tx *sql.Tx) error { _, err := transcript.UndoBatch(ctx, tx, batch); return err })
	var be *transcript.BatchError
	if !errors.As(err, &be) || be.EditID == "" {
		t.Fatalf("batch undo: %v", err)
	}
	if got, _ := text(t, db, w, 0); got != "x the y" {
		t.Fatalf("a refused batch changed the first sentence: %q", got)
	}
}
