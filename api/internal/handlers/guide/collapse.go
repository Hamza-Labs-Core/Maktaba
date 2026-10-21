package guide

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
)

// Block is the per-program payload returned by the JSON guide grid.
// Fields mirror the Story 27.4 contract; most come from the block's
// Snapshot so a read never joins the library.
type Block struct {
	ChannelID string `json:"channel_id"`
	Kind      string `json:"kind"`
	Start     string `json:"start"` // ISO-8601 absolute
	Stop      string `json:"stop"`
	Title     string `json:"title"`
	SubTitle  string `json:"sub_title,omitempty"`
	Desc      string `json:"desc,omitempty"`
	Poster    string `json:"poster,omitempty"`
	Genre     string `json:"genre,omitempty"`
	Rating    string `json:"rating,omitempty"`
	Series    string `json:"series,omitempty"`
	Season    *int   `json:"season,omitempty"`
	Episode   *int   `json:"episode,omitempty"`
	// OnAir is the channel strip's line (Story 27.11), StripTitle /
	// StripSeries / StripEpisode its parts.
	OnAir        string  `json:"on_air,omitempty"`
	StripTitle   string  `json:"strip_title,omitempty"`
	StripSeries  string  `json:"strip_series,omitempty"`
	StripEpisode string  `json:"strip_episode,omitempty"`
	StripRef     string  `json:"strip_ref,omitempty"`
	IsLive       bool    `json:"is_live,omitempty"`
	Progress     float64 `json:"progress,omitempty"`
}

// isFiller reports whether a block kind is filler-ish (collapsed by
// default in the human/external guide, AC10).
func isFiller(kind string) bool {
	return kind == "filler" || kind == "bumper" || kind == "slate"
}

// FillerRules are how the guide shows filler, as dizqueTV's guide
// (guideMaxPaddingSeconds, guideFlexPlaceholder, guideMinimumDurationSeconds).
type FillerRules struct {
	// MaxPadding: a filler run up to this long joins the programme before
	// it (or, with none before it, the one after); a longer one is a block
	// titled FillerTitle (empty: the channel's name).
	MaxPadding  time.Duration
	FillerTitle string
	// MinProgram: programmes shorter than this are not listed; they count
	// as filler.
	MinProgram time.Duration
}

// loadFillerRules reads rules tv.guide_* (the defaults with what was
// changed). A failed read keeps the rules' defaults.
func loadFillerRules(ctx context.Context, db *sql.DB) FillerRules {
	m, err := rules.Effective(ctx, db, "tv")
	if err != nil {
		if m, err = rules.Effective(ctx, nil, "tv"); err != nil {
			return FillerRules{}
		}
	}
	title, _ := m["guide_filler_title"].(string)
	return FillerRules{
		MaxPadding:  time.Duration(ruleInt(m["guide_max_padding_s"])) * time.Second,
		FillerTitle: strings.TrimSpace(title),
		MinProgram:  time.Duration(ruleInt(m["guide_min_program_s"])) * time.Second,
	}
}

func ruleInt(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
	case float64:
		return int64(x)
	case int:
		return int64(x)
	}
	return 0
}

// collapseFiller applies the guide's filler rules (dizqueTV's): filler/
// bumper/slate blocks and programmes shorter than fr.MinProgram are
// filler. A run of adjacent filler up to fr.MaxPadding is folded into
// the programme before it (with none before it, into the one after: a
// short lead-in); a longer run is one "filler" block titled
// fr.FillerTitle (empty: channelName). Liveness is recomputed for a
// block that grew. Input is assumed time-ordered for one channel.
func collapseFiller(blocks []Block, fr FillerRules, channelName string, now time.Time) []Block {
	type span struct {
		b           Block
		start, stop time.Time
		filler      bool
	}
	at := func(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	isFill := func(b Block) bool {
		return isFiller(b.Kind) || at(b.Stop).Sub(at(b.Start)) < fr.MinProgram
	}
	fillerTitle := fr.FillerTitle
	if fillerTitle == "" {
		fillerTitle = channelName
	}
	var out []span
	for i := 0; i < len(blocks); {
		b := blocks[i]
		start, stop := at(b.Start), at(b.Stop)
		var last *span
		if n := len(out); n > 0 && out[n-1].stop.Equal(start) {
			last = &out[n-1]
		}
		if isFill(b) {
			// the whole run of adjacent filler [i, j)
			j := i + 1
			for j < len(blocks) && isFill(blocks[j]) && at(blocks[j].Start).Equal(at(blocks[j-1].Stop)) {
				j++
			}
			stop = at(blocks[j-1].Stop)
			i = j
			if stop.Sub(start) <= fr.MaxPadding && last != nil && !last.filler {
				last.stop = stop
				continue
			}
			out = append(out, span{b: Block{ChannelID: b.ChannelID, Kind: "filler", Title: fillerTitle},
				start: start, stop: stop, filler: true})
			continue
		}
		i++
		if last != nil && last.filler && last.stop.Sub(last.start) <= fr.MaxPadding {
			// A short filler lead-in is absorbed into the programme.
			*last = span{b: b, start: last.start, stop: stop}
			continue
		}
		out = append(out, span{b: b, start: start, stop: stop})
	}
	res := make([]Block, 0, len(out))
	for _, s := range out {
		b := s.b
		b.Start, b.Stop = s.start.UTC().Format(time.RFC3339), s.stop.UTC().Format(time.RFC3339)
		b.IsLive, b.Progress = false, 0
		if !now.Before(s.start) && now.Before(s.stop) {
			b.IsLive, b.Progress = true, progress(s.start, s.stop, now)
		}
		res = append(res, b)
	}
	return res
}
