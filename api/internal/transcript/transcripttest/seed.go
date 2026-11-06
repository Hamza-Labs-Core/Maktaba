// Package transcripttest seeds a scratch Postgres with a video, its active transcript and
// word flags, for the word review tests (Epic 52).
package transcripttest

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript/worddata"
)

// World is what Seed made.
type World struct {
	LibraryID    string
	VideoID      string
	TranscriptID string
	AdminID      string
	OtherAdminID string
}

// Sentence is one transcript_segments row to seed; Words are its whitespace words'
// times (0.5 s each from Start) when WithWords.
type Sentence struct {
	Text      string
	Start     float64
	WithWords bool
}

// WordData packs text's whitespace words, 0.5 s each from the sentence's start.
func WordData(text string) []byte {
	var slots []worddata.Slot
	rs := []rune(text)
	t := 0
	for i := 0; i < len(rs); {
		if rs[i] == ' ' {
			i++
			continue
		}
		j := i
		for j < len(rs) && rs[j] != ' ' {
			j++
		}
		slots = append(slots, worddata.Slot{CharStart: i, CharLen: j - i, StartCS: t, DurCS: 50, Prob: 200})
		t += 50
		i = j
	}
	return worddata.Encode(slots)
}

// Seed makes a library, two home admins, a video and its active transcript.
func Seed(t *testing.T, db *sql.DB, sentences ...Sentence) World {
	t.Helper()
	w := World{LibraryID: uuid.NewString(), VideoID: uuid.NewString(), TranscriptID: uuid.NewString(),
		AdminID: uuid.NewString(), OtherAdminID: uuid.NewString()}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)`, w.LibraryID, "lib-"+w.LibraryID[:8], pq.Array([]string{"/media"}))
	for i, id := range []string{w.AdminID, w.OtherAdminID} {
		exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, $2, 'x', true)`, id, []string{"admin-a", "admin-b"}[i]+id[:4])
	}
	exec(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec)
		VALUES ($1, $2, encode(sha256(convert_to($3, 'UTF8')), 'hex'), '/media/v.mp4', 'v.mp4', 1, now(), 'درس', 600)`,
		w.VideoID, w.LibraryID, w.VideoID)
	var track int64
	if err := db.QueryRow(`INSERT INTO audio_tracks (video_id, track_index) VALUES ($1, 1) RETURNING id`, w.VideoID).Scan(&track); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, word_level, diarized, is_active)
		VALUES ($1, $2, $3, 'ar', 'mlx-batched', 'm', true, false, true)`, w.TranscriptID, w.VideoID, track)
	for seq, s := range sentences {
		var data []byte
		if s.WithWords {
			data = WordData(s.Text)
		}
		exec(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text, confidence, word_data)
			VALUES ($1, $2, $3, $4, $5, 0.9, $6)`, w.TranscriptID, seq, s.Start, s.Start+5, s.Text, data)
	}
	return w
}

// Flag seeds an open word flag on a span of a sentence; it returns its id.
func Flag(t *testing.T, db *sql.DB, w World, seq, charStart, charLen int, original, key, lang string, priority float64, reasons ...string) string {
	t.Helper()
	id := uuid.NewString()
	if len(reasons) == 0 {
		reasons = []string{"not_in_dictionary"}
	}
	var ctx string
	_ = db.QueryRow(`SELECT text FROM transcript_segments WHERE transcript_id = $1 AND seq = $2`, w.TranscriptID, seq).Scan(&ctx)
	if _, err := db.Exec(`INSERT INTO word_flags (id, transcript_id, video_id, library_id, segment_seq, char_start,
		char_len, start_sec, end_sec, original, context, language, reasons, priority, key, candidates)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1.0, 1.5, $8, $9, $10, $11, $12, $13, '[{"text":"x","source":"dictionary","score":0.5}]')`,
		id, w.TranscriptID, w.VideoID, w.LibraryID, seq, charStart, charLen, original, ctx, lang,
		pq.Array(reasons), priority, key); err != nil {
		t.Fatal(err)
	}
	return id
}
