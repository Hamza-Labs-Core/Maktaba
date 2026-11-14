// Package broadcast is the home's side of home broadcasting (Epic 50 story 50.22): the
// Broadcast page's API (Admin → Live TV → Broadcast), the six publish checks, the uplink
// rule, the encoder slots, the calls to Maktaba Cloud through the relay agent (which alone
// holds the home's server secret) and the hourly ingest-token refresh. The broadcaster
// itself (the ladder, the uploader, the control socket) runs in the home's front door
// (streaming/internal/broadcast); the two meet in the broadcast_* tables (slot 0226).
package broadcast

import (
	"fmt"
	"slices"
	"strings"
)

// Rung is one rendition of a published channel's HLS ladder.
type Rung struct {
	// Name is the rung's name at the edge ("src" for the channel's own encode).
	Name string
	// Height is the picture's height (0 for src: the channel's own).
	Height int
	// VideoKbps and AudioKbps are an encoded rung's bitrates (src: the channel's).
	VideoKbps, AudioKbps int
}

// Copy reports whether the rung is the channel's own encode, copied (no encoder session).
func (r Rung) Copy() bool { return r.Name == RungSource }

// RungSource is the top rung: the channel's own encode with its watermark and strip.
const RungSource = "src"

// MaxRungs is the most rungs the edge takes per channel (50.21 §2).
const MaxRungs = 4

// lowerRungs are the encoded rungs, highest first. Streaming's ladder builder has the
// same table (streaming/internal/broadcast/ladder.go); TestRungTableMatchesStreaming
// compares them.
var lowerRungs = []Rung{
	{Name: "720p", Height: 720, VideoKbps: 3000, AudioKbps: 128},
	{Name: "480p", Height: 480, VideoKbps: 1400, AudioKbps: 96},
	{Name: "360p", Height: 360, VideoKbps: 800, AudioKbps: 96},
}

// DefaultLadder is a new channel's ladder: three rungs (README §14.3).
var DefaultLadder = []string{RungSource, "720p", "480p"}

// Source is what the channel's own encode costs: its rules.tv_output bitrates and height.
type Source struct {
	VideoKbps, AudioKbps, Height int
}

// RungByName is a rung of the table (src takes s's bitrates).
func RungByName(name string, s Source) (Rung, bool) {
	if name == RungSource {
		return Rung{Name: RungSource, Height: s.Height, VideoKbps: s.VideoKbps, AudioKbps: s.AudioKbps}, true
	}
	for _, r := range lowerRungs {
		if r.Name == name {
			return r, true
		}
	}
	return Rung{}, false
}

// ValidLadder checks a ladder: 1 to MaxRungs known rungs, highest first, no repeat, and no
// encoded rung as tall as the channel's own picture.
func ValidLadder(names []string, s Source) error {
	if len(names) == 0 || len(names) > MaxRungs {
		return fmt.Errorf("a ladder has 1 to %d rungs", MaxRungs)
	}
	last := 1 << 30
	for i, n := range names {
		r, ok := RungByName(n, s)
		if !ok {
			return fmt.Errorf("unknown rung %q", n)
		}
		if r.Copy() && i != 0 {
			return fmt.Errorf("%s must be the top rung", RungSource)
		}
		h := r.Height
		if r.Copy() {
			h = 1 << 29
		} else if s.Height > 0 && h >= s.Height {
			return fmt.Errorf("rung %s is not below the channel's own %dp", n, s.Height)
		}
		if h >= last {
			return fmt.Errorf("rungs must go from the highest to the lowest")
		}
		last = h
	}
	return nil
}

// TotalKbps is the ladder's bitrates added up.
func TotalKbps(names []string, s Source) int {
	t := 0
	for _, n := range names {
		if r, ok := RungByName(n, s); ok {
			t += r.VideoKbps + r.AudioKbps
		}
	}
	return t
}

// NeedKbps is what a ladder needs of the uplink: its total × 1.1 (§3), rounded up.
func NeedKbps(names []string, s Source) int {
	t := TotalKbps(names, s)
	return (t*11 + 9) / 10
}

// TopKbps is the top rung's bitrate (the plan's MaxTopKbps bounds it).
func TopKbps(names []string, s Source) int {
	if len(names) == 0 {
		return 0
	}
	r, _ := RungByName(names[0], s)
	return r.VideoKbps + r.AudioKbps
}

// Effective is the ladder after the uplink rule dropped `dropped` top rungs (never the last).
func Effective(names []string, dropped int) []string {
	if dropped <= 0 {
		return names
	}
	if dropped >= len(names) {
		dropped = len(names) - 1
	}
	return names[dropped:]
}

// EncodedRungs counts the rungs that take an encoder session (every rung but src).
func EncodedRungs(names []string) int {
	n := 0
	for _, r := range names {
		if r != RungSource {
			n++
		}
	}
	return n
}

// Describe names a ladder for people: "1080p + 720p + 480p" (src as the channel's height).
func Describe(names []string, s Source) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if n == RungSource {
			if s.Height > 0 {
				parts = append(parts, fmt.Sprintf("%dp", s.Height))
			} else {
				parts = append(parts, "source")
			}
			continue
		}
		parts = append(parts, n)
	}
	return strings.Join(parts, " + ")
}

// LadderNames are every rung name the table knows, src first (the page's choices).
func LadderNames() []string {
	out := []string{RungSource}
	for _, r := range lowerRungs {
		out = append(out, r.Name)
	}
	return slices.Clip(out)
}
