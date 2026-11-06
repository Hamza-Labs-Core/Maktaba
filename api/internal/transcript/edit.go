// Package transcript writes a reviewer's word fixes (Epic 52, story 52.9).
//
// A fix is a transcript edit, never a new speech recognition run. In the caller's
// transaction, ApplyEdit locks the sentence, checks its span still reads what the reviewer
// saw, replaces it (offsets in Unicode code points: Go indexes runes), shifts the packed
// word data (worddata.Edit, the same golden cases as the pipeline's Python writer), records
// a transcript_edits row, bumps transcripts.revision, marks the sentence's other open flags
// that overlap stale and moves those after it, and queues subtitle_gen and index for the
// video (the index re-embeds only the edited sentences). Undo and UndoBatch reverse edits
// with the same care: refused when a later edit changed the span.
package transcript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript/worddata"
)

// Errors the review routes turn into 409s.
var (
	ErrStale        = errors.New("stale")         // the span no longer reads what was seen
	ErrNotActive    = errors.New("not_active")    // the transcript was superseded
	ErrChangedSince = errors.New("changed_since") // a later edit touched the span
	ErrNotFound     = errors.New("not_found")
	ErrUndone       = errors.New("already_undone")
)

// Edit asks for one sentence's span (code points) to read After.
type Edit struct {
	TranscriptID string
	SegmentSeq   int
	CharStart    int
	CharLen      int
	Before       string // what the reviewer saw there
	After        string
	FlagID       string // the flag it resolves ("" for none)
	UserID       string
	BatchID      string // a bulk fix's batch ("" for none)
}

// Result is what an edit wrote.
type Result struct {
	EditID  string
	VideoID string
	Text    string
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type sentence struct {
	videoID string
	text    string
	data    []byte
}

func lockSentence(ctx context.Context, tx *sql.Tx, transcriptID string, seq int) (sentence, error) {
	var s sentence
	var active bool
	err := tx.QueryRowContext(ctx,
		`SELECT t.video_id::text, t.is_active FROM transcripts t WHERE t.id = $1 FOR UPDATE`,
		transcriptID).Scan(&s.videoID, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if !active {
		return s, ErrNotActive
	}
	err = tx.QueryRowContext(ctx,
		`SELECT text, word_data FROM transcript_segments WHERE transcript_id = $1 AND seq = $2 FOR UPDATE`,
		transcriptID, seq).Scan(&s.text, &s.data)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func span(text string, start, n int) (string, bool) {
	rs := []rune(text)
	if start < 0 || n < 0 || start+n > len(rs) {
		return "", false
	}
	return string(rs[start : start+n]), true
}

// write replaces the sentence's span, shifts the word data and the flags after it, bumps
// the revision and queues the follow-ups.
func write(ctx context.Context, tx *sql.Tx, s sentence, transcriptID string, seq, start, n int, after string) (string, error) {
	text, data, err := worddata.Edit(s.text, s.data, start, n, after)
	if errors.Is(err, worddata.ErrSpan) {
		return "", ErrStale
	}
	if err != nil {
		// word data that cannot be read: the text is the truth, the words go
		text, _, err = worddata.Edit(s.text, nil, start, n, after)
		if err != nil {
			return "", err
		}
		data = nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE transcript_segments SET text = $3, word_data = $4 WHERE transcript_id = $1 AND seq = $2`,
		transcriptID, seq, text, data); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE transcripts SET revision = revision + 1 WHERE id = $1`, transcriptID); err != nil {
		return "", err
	}
	if err := shiftFlags(ctx, tx, transcriptID, seq, start, n, len([]rune(after))-n); err != nil {
		return "", err
	}
	if err := followUps(ctx, tx, s.videoID, transcriptID, seq); err != nil {
		return "", err
	}
	return text, nil
}

// shiftFlags: the sentence's open flags that overlap the edit go stale, those after it
// move by delta (one row at a time, in the order that keeps the places unique).
func shiftFlags(ctx context.Context, tx *sql.Tx, transcriptID string, seq, start, n, delta int) error {
	end := start + n
	if _, err := tx.ExecContext(ctx, `UPDATE word_flags SET status = 'stale'
		WHERE transcript_id = $1 AND segment_seq = $2 AND status = 'open'
		  AND char_start < $4 AND char_start + char_len > $3`,
		transcriptID, seq, start, max(end, start+1)); err != nil {
		return err
	}
	if delta == 0 {
		return nil
	}
	order := "DESC"
	if delta < 0 {
		order = "ASC"
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM word_flags
		WHERE transcript_id = $1 AND segment_seq = $2 AND char_start >= $3 ORDER BY char_start `+order,
		transcriptID, seq, end)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE word_flags SET char_start = char_start + $2 WHERE id = $1`, id, delta); err != nil {
			return err
		}
	}
	return nil
}

// followUps queues the video's subtitle files and its one sentence's vector again; a
// burst of fixes coalesces into one job each.
func followUps(ctx context.Context, tx *sql.Tx, videoID, transcriptID string, seq int) error {
	reason := map[string]any{"why": "word_fix", "by": "review"}
	if _, err := queues.Enqueue(ctx, tx, queues.EnqueueParams{
		SubjectKind: "video", SubjectID: videoID, Stage: "subtitle_gen", Reason: reason,
	}); err != nil {
		return fmt.Errorf("enqueue subtitle_gen: %w", err)
	}
	if _, err := queues.Enqueue(ctx, tx, queues.EnqueueParams{
		SubjectKind: "video", SubjectID: videoID, Stage: "index", Reason: reason,
		Payload: map[string]any{"transcript_id": transcriptID, "seqs": []int{seq}},
	}); err != nil {
		return fmt.Errorf("enqueue index: %w", err)
	}
	return nil
}

// ApplyEdit applies e in tx. ErrStale: the span no longer reads e.Before (the flag, when
// there is one, is marked stale); ErrNotActive: the transcript was superseded.
func ApplyEdit(ctx context.Context, tx *sql.Tx, e Edit) (Result, error) {
	s, err := lockSentence(ctx, tx, e.TranscriptID, e.SegmentSeq)
	if err != nil {
		return Result{}, err
	}
	if got, ok := span(s.text, e.CharStart, e.CharLen); !ok || got != e.Before {
		if e.FlagID != "" {
			_, _ = tx.ExecContext(ctx, `UPDATE word_flags SET status = 'stale' WHERE id = $1 AND status = 'open'`, e.FlagID)
		}
		return Result{}, ErrStale
	}
	text, err := write(ctx, tx, s, e.TranscriptID, e.SegmentSeq, e.CharStart, e.CharLen, e.After)
	if err != nil {
		return Result{}, err
	}
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO transcript_edits
		(id, transcript_id, segment_seq, char_start, char_len, before, after, flag_id, user_id, batch_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, e.TranscriptID, e.SegmentSeq, e.CharStart, e.CharLen, e.Before, e.After,
		nullable(e.FlagID), nullable(e.UserID), nullable(e.BatchID)); err != nil {
		return Result{}, err
	}
	return Result{EditID: id, VideoID: s.videoID, Text: text}, nil
}

// Undo reverses one edit in tx: its span must still read what it wrote. The flag it
// resolved is open again.
func Undo(ctx context.Context, tx *sql.Tx, editID string) error {
	var tid, before, after string
	var seq, start int
	var flagID sql.NullString
	var undone sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT transcript_id::text, segment_seq, char_start, before, after,
		flag_id::text, undone_at FROM transcript_edits WHERE id = $1 FOR UPDATE`, editID).
		Scan(&tid, &seq, &start, &before, &after, &flagID, &undone)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if undone.Valid {
		return ErrUndone
	}
	s, err := lockSentence(ctx, tx, tid, seq)
	if err != nil {
		return err
	}
	n := len([]rune(after))
	if got, ok := span(s.text, start, n); !ok || got != after {
		return ErrChangedSince
	}
	// a later live edit that touched the span (even one that left it reading the same
	// at its start) changed it since
	var later bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM transcript_edits l, transcript_edits e
		WHERE e.id = $1 AND l.transcript_id = e.transcript_id AND l.segment_seq = e.segment_seq
		  AND l.undone_at IS NULL AND l.id <> e.id AND (l.created_at, l.id::text) > (e.created_at, e.id::text)
		  AND l.char_start < e.char_start + GREATEST($2, 1) AND l.char_start + GREATEST(l.char_len, 1) > e.char_start)`,
		editID, n).Scan(&later); err != nil {
		return err
	}
	if later {
		return ErrChangedSince
	}
	if _, err := write(ctx, tx, s, tid, seq, start, n, before); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE transcript_edits SET undone_at = now() WHERE id = $1`, editID); err != nil {
		return err
	}
	if flagID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE word_flags SET status = 'open', resolution = NULL,
			resolved_by = NULL, resolved_at = NULL WHERE id = $1`, flagID.String); err != nil {
			return err
		}
	}
	return nil
}

// BatchError names the edit of a batch that could not be undone.
type BatchError struct {
	EditID string
	Err    error
}

func (e *BatchError) Error() string { return fmt.Sprintf("edit %s: %v", e.EditID, e.Err) }
func (e *BatchError) Unwrap() error { return e.Err }

// UndoBatch reverses every live edit of a bulk fix, newest first; all or nothing (the
// caller rolls tx back on an error, which names the edit). Flags the batch resolved
// without an edit (accept, name, dictionary) are opened again too.
func UndoBatch(ctx context.Context, tx *sql.Tx, batchID string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM transcript_edits
		WHERE batch_id = $1 AND undone_at IS NULL ORDER BY created_at DESC, id DESC`, batchID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := Undo(ctx, tx, id); err != nil {
			return 0, &BatchError{EditID: id, Err: err}
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE word_flags SET status = 'open', resolution = NULL,
		resolved_by = NULL, resolved_at = NULL, scores = scores - 'batch'
		WHERE scores ->> 'batch' = $1 AND status IN ('accepted', 'name', 'dictionary', 'skipped')`, batchID)
	if err != nil {
		return 0, err
	}
	reopened, _ := res.RowsAffected()
	return len(ids) + int(reopened), nil
}
