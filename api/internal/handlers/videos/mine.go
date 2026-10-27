package videos

// Epic 45 Phase 2 on the video list: the caller's own marks on each item
// (playback and favorites), the A to Z jump bar's letter filter and the
// Shuffle sort.

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
)

// sortRandom is GET /api/videos?sort=random: one page in a random order.
const sortRandom = "random"

// titleKey is the title the list sorts and jumps by (videos_library_title_idx).
const titleKey = "COALESCE(title, '')"

// UserTablesHere tells whether this request may read a user's own tables
// (favorites and playlists, slot 0162). A machine serving an `any` read from
// its copy has none: the copy leaves them out, as the pins.
func UserTablesHere(ctx context.Context) bool {
	return placement.ClassFrom(ctx) != placement.Any
}

// IsLetterFirst tells whether a title's first character (as the database's
// substr gives it) is a letter; every other one is the jump bar's "#".
func IsLetterFirst(first string) bool {
	r, _ := utf8.DecodeRuneInString(first)
	return first != "" && r != utf8.RuneError && unicode.IsLetter(r)
}

// letterCond is the WHERE fragment of ?letter=: titles whose first character
// is the letter (in any case), or for "#" whose first character is not a
// letter (an empty title among them). A letter is a range on the title (the
// index reads it) and the exact first character (any collation's order).
// "#" lists the scope's first characters that are not letters first.
func (h *Handler) letterCond(ctx context.Context, letter string, conds []string, args []any, next int) (string, []any, error) {
	firsts := []string{}
	if letter == "#" {
		where := ""
		if len(conds) > 0 {
			where = " WHERE " + strings.Join(conds, " AND ")
		}
		rows, err := h.DB.QueryContext(ctx, `SELECT DISTINCT substr(`+titleKey+`, 1, 1) FROM videos`+where, args...)
		if err != nil {
			return "", nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var f sql.NullString
			if err := rows.Scan(&f); err != nil {
				return "", nil, err
			}
			if !IsLetterFirst(f.String) {
				firsts = append(firsts, f.String)
			}
		}
		if err := rows.Err(); err != nil {
			return "", nil, err
		}
		if len(firsts) == 0 {
			return "1 = 0", nil, nil
		}
		ph := make([]string, len(firsts))
		out := make([]any, len(firsts))
		for i, f := range firsts {
			ph[i] = pidx(next + i)
			out[i] = f
		}
		return "substr(" + titleKey + ", 1, 1) IN (" + strings.Join(ph, ",") + ")", out, nil
	}
	r, _ := utf8.DecodeRuneInString(letter)
	seen := map[rune]bool{}
	for _, c := range []rune{r, unicode.ToUpper(r), unicode.ToLower(r)} {
		if !seen[c] {
			seen[c] = true
			firsts = append(firsts, string(c))
		}
	}
	var out []any
	ranges := make([]string, len(firsts))
	in := make([]string, len(firsts))
	for i, f := range firsts {
		c, _ := utf8.DecodeRuneInString(f)
		ranges[i] = "(" + titleKey + " >= " + pidx(next+len(out)) + " AND " + titleKey + " < " + pidx(next+len(out)+1) + ")"
		out = append(out, f, string(c+1))
	}
	for i, f := range firsts {
		in[i] = pidx(next + len(out))
		out = append(out, f)
	}
	return "((" + strings.Join(ranges, " OR ") + ") AND substr(" + titleKey + ", 1, 1) IN (" + strings.Join(in, ",") + "))", out, nil
}

// mineColumns are the list's extra columns for the caller's own marks: their
// playback (position, completed, when) and whether the video is one of their
// favorites (NULL where a machine's copy has no favorites), each a read on a
// primary key in the list's own statement. None for a principal without a
// user row (the admin token).
func mineColumns(userID string, userTables bool, args *[]any) (string, bool) {
	if _, err := uuid.Parse(userID); err != nil {
		return "", false
	}
	*args = append(*args, userID)
	u := pidx(len(*args))
	ps := func(col string) string {
		return "(SELECT ps." + col + " FROM playback_state ps WHERE ps.user_id = " + u + " AND ps.video_id = videos.id)"
	}
	fav := "NULL"
	if userTables {
		fav = "CASE WHEN EXISTS (SELECT 1 FROM user_favorites uf WHERE uf.user_id = " + u +
			" AND uf.video_id = videos.id) THEN 1 ELSE 0 END"
	}
	// named apart: an output column called updated_at would make the list's
	// ORDER BY updated_at ambiguous on Postgres
	return ", " + ps("position_sec") + " AS mine_position, " + ps("completed") + " AS mine_completed, " +
		ps("updated_at") + " AS mine_at, " + fav + " AS mine_favorite", true
}

// mineRow holds the mineColumns of a row.
type mineRow struct {
	pos      sql.NullFloat64
	done     sql.NullBool
	at       nullTime
	favorite sql.NullInt64
}

func (m *mineRow) dest() []any { return []any{&m.pos, &m.done, &m.at, &m.favorite} }

func (m *mineRow) apply(v *Video) {
	if m.pos.Valid {
		v.PlaybackState = &PlaybackState{PositionSec: m.pos.Float64, Completed: m.done.Valid && m.done.Bool, UpdatedAt: m.at.T}
	}
	if m.favorite.Valid && m.favorite.Int64 == 1 {
		yes := true
		v.Favorite = &yes
	}
}
