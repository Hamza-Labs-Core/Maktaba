package review

import (
	"context"
	"database/sql"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

// PairsToWord is how many times the same correction is made before its spelling joins
// the vocabulary (story 52.10).
const PairsToWord = 3

// Key folds a word the way the packs' keys do for the languages the API can tell apart
// (the pipeline's packs hold the exact rules; a flag carries its pack's own key, and this
// is only for words a person typed): NFC, harakat, tatweel and bidi marks removed, the
// Arabic letter variants unified, lower case.
func Key(s string) string {
	s = norm.NFC.String(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 0x064B && r <= 0x065F, r == 0x0670, r >= 0x06D6 && r <= 0x06ED, r == 0x0640,
			r >= 0x200C && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			continue
		case unicode.IsSpace(r):
			continue
		}
		switch r {
		case 'أ', 'إ', 'آ', 'ٱ':
			r = 'ا'
		case 'ة':
			r = 'ه'
		case 'ى', 'ئ', 'ی':
			r = 'ي'
		case 'ؤ':
			r = 'و'
		case 'ک':
			r = 'ك'
		}
		if r >= '٠' && r <= '٩' {
			r = '0' + (r - '٠')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// learn records what the resolution teaches: a name or a dictionary word joins the
// vocabulary; a correction is a "heard X, meant Y" pair, and after PairsToWord of them the
// spelling joins the vocabulary too. Every new vocabulary key re-checks the transcripts
// with an open flag of that key (never the whole library).
func (h *Handler) learn(ctx context.Context, tx *sql.Tx, f Flag, body ResolveBody, user, editID string) error {
	switch body.Action {
	case "name", "dictionary":
		term := body.Text
		if term == "" {
			term = f.Original
		}
		kind := "word"
		if body.Action == "name" {
			kind = "name"
		}
		scope, lib := "library", any(f.LibraryID)
		if body.Scope == "home" {
			scope, lib = "home", nil
		}
		key := f.Key
		if body.Text != "" {
			key = Key(term)
		}
		if err := addVocab(ctx, tx, scope, lib, f.Language, term, key, kind, "review", user); err != nil {
			return err
		}
		return rescore(ctx, tx, f.Key, f.Language)
	case "correct":
		var uses int
		err := tx.QueryRowContext(ctx, `INSERT INTO vocab_pairs (id, library_id, language, heard, heard_key, meant, uses, last_edit_id)
			VALUES ($1, $2, $3, $4, $5, $6, 1, $7)
			ON CONFLICT (owner, language, heard_key, meant)
			DO UPDATE SET uses = vocab_pairs.uses + 1, last_edit_id = excluded.last_edit_id, heard = excluded.heard
			RETURNING uses`,
			uuid.NewString(), f.LibraryID, f.Language, f.Original, f.Key, body.Text, nullable(editID)).Scan(&uses)
		if err != nil {
			return err
		}
		if uses >= PairsToWord {
			if err := addVocab(ctx, tx, "library", f.LibraryID, f.Language, body.Text, Key(body.Text), "word", "review", user); err != nil {
				return err
			}
		}
		return rescore(ctx, tx, f.Key, f.Language)
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func addVocab(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, scope string, lib any, lang, term, key, kind, source, user string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO vocabulary (id, scope, library_id, language, term, key, kind, source, uses, added_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9)
		ON CONFLICT (scope, owner, language, key, term) DO UPDATE SET uses = vocabulary.uses + 1`,
		uuid.NewString(), scope, lib, lang, term, key, kind, source, nullable(user))
	return err
}

// rescore queues word_check for the videos with an open flag of key in language.
func rescore(ctx context.Context, tx *sql.Tx, key, lang string) error {
	if key == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT video_id::text FROM word_flags
		WHERE key = $1 AND language = $2 AND status IN ('open', 'accepted') LIMIT 5000`, key, lang)
	if err != nil {
		return err
	}
	var vids []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			vids = append(vids, v)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	prio := 600
	for _, v := range vids {
		if _, err := queues.Enqueue(ctx, tx, queues.EnqueueParams{
			SubjectKind: "video", SubjectID: v, Stage: "word_check", Priority: &prio,
			Reason: map[string]any{"why": "vocabulary", "by": "review"},
		}); err != nil {
			return err
		}
	}
	return nil
}
