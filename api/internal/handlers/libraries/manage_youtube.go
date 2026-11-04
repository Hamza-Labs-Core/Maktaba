package libraries

// Each library's YouTube on Manage libraries (slot 0175): how many sources it follows
// and how many are on, the source itself when it has one (the card's switch), and,
// for a library with none, the channel its files look like (youtube_source_guesses,
// written by POST /api/libraries/{id}/youtube/guess). Slot 0182: the pipeline derives
// each library's sources from its linked videos (youtube_source_decisions); a derived
// source says so, and a library whose channel or playlist is another library's source
// (a clash) shows that as its guess, with the library that follows it (the duplicate
// hint). Three queries for every library at once, beside Manage's three.

import (
	"context"
	"database/sql"
	"time"
)

// YouTubeSourceBrief is a library's one source, for the card's switch.
type YouTubeSourceBrief struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	ExternalID string  `json:"external_id"`
	URL        string  `json:"url"`
	Mode       string  `json:"mode"`
	LastOnMode *string `json:"last_on_mode"`
	// Derived: the pipeline made it from the library's linked videos (slot 0182), off.
	Derived bool `json:"derived"`
}

// LibraryRef names a library.
type LibraryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// YouTubeGuess is the channel or playlist a library's files look like; Kind,
// ExternalID and URL are nil when the API looked and found none.
type YouTubeGuess struct {
	Kind            *string   `json:"kind"`
	ExternalID      *string   `json:"external_id"`
	URL             *string   `json:"url"`
	Title           *string   `json:"title"`
	Evidence        *string   `json:"evidence"`
	SampleYouTubeID *string   `json:"sample_youtube_id"`
	IDsFound        int       `json:"ids_found"`
	IDsMatched      int       `json:"ids_matched"`
	LookedAt        time.Time `json:"looked_at"`
	// FollowedBy are the libraries that already follow it.
	FollowedBy []LibraryRef `json:"followed_by"`
	// Derived: from the library's linked videos (slot 0182, youtube_source_decisions):
	// its channel or playlist is another library's source (Evidence "linked").
	Derived bool `json:"derived"`
}

// LibraryYouTube is a library's YouTube state on its card.
type LibraryYouTube struct {
	Sources int `json:"sources"`
	On      int `json:"on"`
	// Source is the library's source when it has exactly one.
	Source *YouTubeSourceBrief `json:"source"`
	// Guess is set only for a library with no source.
	Guess *YouTubeGuess `json:"guess"`
}

func ptr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// youTubeStates is every library's YouTube state, by library id.
func (h *Handler) youTubeStates(ctx context.Context, names map[string]string) (map[string]*LibraryYouTube, error) {
	rows, err := h.DB.QueryContext(ctx, `
		SELECT id, library_id, kind, external_id, url, mode, last_on_mode, derived_at IS NOT NULL
		  FROM youtube_sources ORDER BY library_id, added_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*LibraryYouTube{}
	followers := map[string][]LibraryRef{} // external id -> libraries
	for rows.Next() {
		var s YouTubeSourceBrief
		var lib string
		var lastOn sql.NullString
		if err := rows.Scan(&s.ID, &lib, &s.Kind, &s.ExternalID, &s.URL, &s.Mode, &lastOn, &s.Derived); err != nil {
			return nil, err
		}
		s.LastOnMode = ptr(lastOn)
		y := out[lib]
		if y == nil {
			y = &LibraryYouTube{}
			out[lib] = y
		}
		y.Sources++
		if s.Mode != "off" {
			y.On++
		}
		if y.Sources == 1 {
			s := s
			y.Source = &s
		} else {
			y.Source = nil
		}
		followers[s.ExternalID] = append(followers[s.ExternalID], LibraryRef{ID: lib, Name: names[lib]})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	g, err := h.DB.QueryContext(ctx, `
		SELECT library_id, kind, external_id, url, title, evidence, sample_youtube_id, ids_found,
		       ids_matched, looked_at
		  FROM youtube_source_guesses`)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	for g.Next() {
		var lib string
		var kind, ext, url, title, ev, sample sql.NullString
		var gs YouTubeGuess
		if err := g.Scan(&lib, &kind, &ext, &url, &title, &ev, &sample, &gs.IDsFound, &gs.IDsMatched,
			&gs.LookedAt); err != nil {
			return nil, err
		}
		if y := out[lib]; y != nil && y.Sources > 0 {
			continue // it has a source now: the guess is moot
		}
		gs.Kind, gs.ExternalID, gs.URL, gs.Title, gs.Evidence, gs.SampleYouTubeID =
			ptr(kind), ptr(ext), ptr(url), ptr(title), ptr(ev), ptr(sample)
		gs.FollowedBy = []LibraryRef{}
		if gs.ExternalID != nil {
			gs.FollowedBy = append(gs.FollowedBy, followers[*gs.ExternalID]...)
		}
		y := out[lib]
		if y == nil {
			y = &LibraryYouTube{}
			out[lib] = y
		}
		y.Guess = &gs
	}
	if err := g.Err(); err != nil {
		return nil, err
	}
	g.Close()
	return out, h.clashGuesses(ctx, out, followers)
}

// clashGuesses gives a library with no source and no guess of its own the channel or
// playlist its linked videos come from when that is another library's source (the
// derivation's clash, slot 0182): the card then shows who follows it, and Merge.
func (h *Handler) clashGuesses(ctx context.Context, out map[string]*LibraryYouTube,
	followers map[string][]LibraryRef) error {
	rows, err := h.DB.QueryContext(ctx, `
		SELECT d.library_id, d.kind, d.clash_external_id, d.linked_videos, d.matched_videos,
		       d.decided_at, COALESCE(c.title, p.title, '')
		  FROM youtube_source_decisions d
		  LEFT JOIN youtube_channels c ON d.kind = 'channel' AND c.channel_id = d.clash_external_id
		  LEFT JOIN youtube_playlists p ON d.kind = 'playlist' AND p.playlist_id = d.clash_external_id
		 WHERE d.outcome = 'clash' AND d.clash_external_id IS NOT NULL AND d.kind IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lib, kind, ext, title string
		gs := YouTubeGuess{Derived: true}
		if err := rows.Scan(&lib, &kind, &ext, &gs.IDsFound, &gs.IDsMatched, &gs.LookedAt, &title); err != nil {
			return err
		}
		y := out[lib]
		if y != nil && (y.Sources > 0 || y.Guess != nil) {
			continue // it has a source, or an admin's look said what it looks like
		}
		url := "https://www.youtube.com/channel/" + ext
		if kind == "playlist" {
			url = "https://www.youtube.com/playlist?list=" + ext
		}
		evidence := "linked"
		gs.Kind, gs.ExternalID, gs.URL, gs.Evidence = &kind, &ext, &url, &evidence
		if title != "" {
			gs.Title = &title
		}
		gs.FollowedBy = append([]LibraryRef{}, followers[ext]...)
		if y == nil {
			y = &LibraryYouTube{}
			out[lib] = y
		}
		y.Guess = &gs
	}
	return rows.Err()
}
