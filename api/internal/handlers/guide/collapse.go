package guide

// GuideBlock is the per-program payload returned by the JSON guide grid.
// Fields mirror the Story 27.4 contract; most come from the block's
// Snapshot so a read never joins the library.
type GuideBlock struct {
	ChannelID string  `json:"channel_id"`
	Kind      string  `json:"kind"`
	Start     string  `json:"start"` // ISO-8601 absolute
	Stop      string  `json:"stop"`
	Title     string  `json:"title"`
	SubTitle  string  `json:"sub_title,omitempty"`
	Desc      string  `json:"desc,omitempty"`
	Poster    string  `json:"poster,omitempty"`
	Genre     string  `json:"genre,omitempty"`
	Rating    string  `json:"rating,omitempty"`
	Series    string  `json:"series,omitempty"`
	Season    *int    `json:"season,omitempty"`
	Episode   *int    `json:"episode,omitempty"`
	IsLive    bool    `json:"is_live,omitempty"`
	Progress  float64 `json:"progress,omitempty"`
}

// isFiller reports whether a block kind is filler-ish (collapsed by
// default in the human/external guide, AC10).
func isFiller(kind string) bool {
	return kind == "filler" || kind == "bumper" || kind == "slate"
}

// collapseFiller merges runs of adjacent filler/bumper/slate blocks into
// a single "Up Next" placeholder so the guide isn't littered with
// 15-second rows (AC10 / TC9). Program blocks pass through untouched.
// Input is assumed time-ordered for one channel.
func collapseFiller(blocks []GuideBlock) []GuideBlock {
	out := make([]GuideBlock, 0, len(blocks))
	i := 0
	for i < len(blocks) {
		b := blocks[i]
		if !isFiller(b.Kind) {
			out = append(out, b)
			i++
			continue
		}
		// Coalesce the whole filler run [i, j) into one block spanning
		// from the first start to the last stop.
		j := i
		for j < len(blocks) && isFiller(blocks[j].Kind) {
			j++
		}
		merged := GuideBlock{
			ChannelID: b.ChannelID,
			Kind:      "filler",
			Start:     b.Start,
			Stop:      blocks[j-1].Stop,
			Title:     "Up Next",
		}
		// Preserve liveness/progress if the live block falls in the run.
		for k := i; k < j; k++ {
			if blocks[k].IsLive {
				merged.IsLive = true
				merged.Progress = blocks[k].Progress
			}
		}
		out = append(out, merged)
		i = j
	}
	return out
}
