package parental

// The age tier an admin set, as the GET of the thing it was set on carries it
// (Epic 57): the web's rating control shows the current value instead of asking
// the admin to choose again. Only an admin gets the field. For everyone else it is
// absent: a tier an admin set is theirs to see and change, and a restricted
// account is never told how its content is rated.

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// AdminTier is what an admin set: a tier, or JSON null when nothing is set. A
// handler leaves the pointer nil for a reader who is not an admin, so the field
// is absent from their answer.
type AdminTier struct {
	Tier *int
}

// MarshalJSON writes the tier, or null.
func (t AdminTier) MarshalJSON() ([]byte, error) {
	if t.Tier == nil {
		return []byte("null"), nil
	}
	return strconv.AppendInt(nil, int64(*t.Tier), 10), nil
}

// What an admin's tier is read from. A video's is the admin's own
// (age_tier_admin): videos.age_tier is what the video resolves to, from any source.
const (
	TierOnVideo         = "video"
	TierOnLibrary       = "library"
	TierOnSeries        = "series"
	TierOnCollection    = "collection"
	TierOnChannel       = "channel"
	TierOnYouTubeSource = "youtube_source"
)

// adminTierOne reads one row's tier; the id is compared as text, so a malformed
// id finds nothing instead of failing.
var adminTierOne = map[string]string{
	TierOnVideo:         `SELECT age_tier_admin FROM videos WHERE id::text = $1`,
	TierOnLibrary:       `SELECT age_tier FROM libraries WHERE id::text = $1`,
	TierOnSeries:        `SELECT age_tier FROM series WHERE id::text = $1`,
	TierOnCollection:    `SELECT age_tier FROM collections WHERE id::text = $1`,
	TierOnChannel:       `SELECT age_tier FROM channels WHERE id::text = $1`,
	TierOnYouTubeSource: `SELECT age_tier FROM youtube_sources WHERE id::text = $1`,
}

// adminTierAll reads every row's tier: $1 is the library of a YouTube source, and
// unused (” matches every row) for the kinds a list answers whole.
var adminTierAll = map[string]string{
	TierOnLibrary:       `SELECT id::text, age_tier FROM libraries WHERE $1 = ''`,
	TierOnChannel:       `SELECT id::text, age_tier FROM channels WHERE $1 = ''`,
	TierOnYouTubeSource: `SELECT id::text, age_tier FROM youtube_sources WHERE library_id::text = $1`,
}

// AdminTierOf is the tier an admin set on one thing, for p's answer: nil (the
// field is absent) unless p is an admin, and when it cannot be read (the control
// then shows no current value, and the admin's next save still holds).
func AdminTierOf(ctx context.Context, db *sql.DB, p *principal.Principal, kind, id string) *AdminTier {
	q, ok := adminTierOne[kind]
	if !ok || db == nil || p == nil || !p.IsAdmin {
		return nil
	}
	var tier sql.NullInt64
	if err := db.QueryRowContext(ctx, q, id).Scan(&tier); err != nil {
		return nil
	}
	return &AdminTier{Tier: intPtr(tier)}
}

// AdminTiers is AdminTierOf for a list: the tier of every library or channel
// (parent ""), or of every YouTube source of the library parent, by id. It is nil
// unless p is an admin, and when the tiers cannot be read.
func AdminTiers(ctx context.Context, db *sql.DB, p *principal.Principal, kind, parent string) map[string]*AdminTier {
	q, ok := adminTierAll[kind]
	if !ok || db == nil || p == nil || !p.IsAdmin {
		return nil
	}
	rows, err := db.QueryContext(ctx, q, parent)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]*AdminTier{}
	for rows.Next() {
		var (
			id   string
			tier sql.NullInt64
		)
		if err := rows.Scan(&id, &tier); err != nil {
			return nil
		}
		out[id] = &AdminTier{Tier: intPtr(tier)}
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}
