package parental

import (
	"context"
	"database/sql"
	"errors"
)

// SubjectInfo is one subject as a guardian's list shows it. A subject that is no
// longer there has Removed set and nothing else.
type SubjectInfo struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Title is the subject's name (a video's title, or its file name when it has none).
	Title string `json:"title,omitempty"`
	// PosterURL is the path of the poster, for a video.
	PosterURL string `json:"poster_url,omitempty"`
	// Tier is the subject's age tier; absent when it is unrated.
	Tier *int `json:"tier,omitempty"`
	// Removed is set when the subject was deleted since.
	Removed bool `json:"removed,omitempty"`
	// Hidden is set when the reader cannot open the subject's library: the title,
	// the poster and the tier are left out.
	Hidden bool `json:"hidden,omitempty"`
	// LibraryID is the library the subject sits in ("" for none: a plugin, a channel
	// over several libraries). Not sent: the handlers decide Hidden with it.
	LibraryID string `json:"-"`
}

// subjectQueries reads a subject's title, library, tier and whether it has a poster.
// The id is compared as text, so a malformed id finds nothing instead of failing.
var subjectQueries = map[string]string{
	SubjectVideo: `SELECT COALESCE(NULLIF(title, ''), filename), library_id::text, age_tier, poster_path IS NOT NULL
		FROM videos WHERE id::text = $1`,
	SubjectSeries: `SELECT COALESCE(NULLIF(name_override, ''), name), COALESCE(library_id::text, ''), age_tier, false
		FROM series WHERE id::text = $1`,
	SubjectCollection: `SELECT COALESCE(NULLIF(name_override, ''), name), library_id::text, age_tier, false
		FROM collections WHERE id::text = $1`,
	SubjectLibrary: `SELECT name, id::text, age_tier, false FROM libraries WHERE id::text = $1`,
	SubjectChannel: `SELECT name, COALESCE(library_id::text, ''), age_tier, false FROM channels WHERE id::text = $1`,
	SubjectYouTubeSource: `SELECT COALESCE(NULLIF(folder, ''), url), library_id::text, age_tier, false
		FROM youtube_sources WHERE id::text = $1`,
	SubjectPlugin: `SELECT COALESCE(NULLIF(manifest ->> 'name', ''), plugin_id), '', NULL::smallint, false
		FROM plugin_installs WHERE plugin_id = $1`,
}

// Subject reads one subject. A subject that is not there (or a kind this package
// does not know) comes back with Removed set, not as an error.
func (s *Store) Subject(ctx context.Context, kind, id string) (SubjectInfo, error) {
	return subjectOf(ctx, s.DB, kind, id)
}

func subjectOf(ctx context.Context, q queryer, kind, id string) (SubjectInfo, error) {
	out := SubjectInfo{Kind: kind, ID: id}
	query, ok := subjectQueries[kind]
	if !ok {
		out.Removed = true
		return out, nil
	}
	var (
		tier   sql.NullInt64
		poster bool
	)
	err := q.QueryRowContext(ctx, query, id).Scan(&out.Title, &out.LibraryID, &tier, &poster)
	if errors.Is(err, sql.ErrNoRows) {
		out.Removed = true
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Tier = intPtr(tier)
	if poster && kind == SubjectVideo {
		out.PosterURL = "/api/videos/" + id + "/poster"
	}
	return out, nil
}

// For is s as a reader with libraries (all: every library) may see it: a subject
// in a library the reader cannot open keeps its kind and id only.
func (s SubjectInfo) For(all bool, libraries []string) SubjectInfo {
	if s.Removed || all || s.LibraryID == "" {
		return s
	}
	for _, l := range libraries {
		if l == s.LibraryID {
			return s
		}
	}
	return SubjectInfo{Kind: s.Kind, ID: s.ID, Hidden: true, LibraryID: s.LibraryID}
}
