// Package guide implements Story 27.4 — the EPG read surface over
// channel_programs:
//
//	GET /api/channels/guide?start=&end=&category=   grid: all channels × time
//	GET /api/channels/{id}/guide?start=&end=         one channel (+ horizon marker)
//	GET /api/channels/now                            current+next per channel
//	GET /api/channels/xmltv                          XMLTV XML export
//	GET /api/channels/playlist.m3u                   M3U playlist (VLC/IPTV)
//
// The guide is a pure read path: every output is a time-range query over
// channel_programs (Story 27.2's output); there is no separate guide
// store. Block display metadata comes from the block's cached
// `title_snapshot` so a guide read never joins the whole library.
package guide

import "encoding/json"

// Snapshot is the typed view of a channel_programs.title_snapshot blob.
// It is the cross-language contract with the Python scheduler (Plan 27.2
// D8 / AC11): the packer writes these keys, the guide reads them. All
// fields are optional — a degenerate block may carry only a title.
type Snapshot struct {
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	Poster       string `json:"poster,omitempty"`
	Genre        string `json:"genre,omitempty"`
	Rating       string `json:"rating,omitempty"`
	Series       string `json:"series,omitempty"`
	Season       *int   `json:"season,omitempty"`
	Episode      *int   `json:"episode,omitempty"`
	EpisodeTitle string `json:"episode_title,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
}

// parseSnapshot decodes a title_snapshot blob. A nil/empty/garbage blob
// yields a zero Snapshot rather than an error — the guide must stay
// usable even if a block's metadata is missing.
func parseSnapshot(raw []byte) Snapshot {
	var s Snapshot
	if len(raw) == 0 {
		return s
	}
	_ = json.Unmarshal(raw, &s)
	return s
}

// IsEpisodic reports whether the block has both a season and an episode
// number — the precondition for emitting XMLTV <episode-num> elements.
func (s Snapshot) IsEpisodic() bool {
	return s.Season != nil && s.Episode != nil
}
