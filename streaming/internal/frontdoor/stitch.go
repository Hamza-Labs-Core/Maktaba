package frontdoor

import (
	"bufio"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The HLS stitcher (Epic 33 design §8.4): with TV slots the front door serves a
// channel's live playlist itself. It lists the segments of the slot the channel
// is routed to under /stream/channel/{id}/s/{slot}/…, with its own media
// sequence. When the routing moves to another slot (a hot-standby failover now,
// a swap or rollback with story 33.7) the next segment comes from the new slot:
// the first one whose PROGRAM-DATE-TIME starts at (or after) the end of the
// last one listed, after one EXT-X-DISCONTINUITY. With splice-ready output
// (story 33.4) that segment starts exactly there, so the viewer sees one
// discontinuity and no rewind or skip. EXT-X-DISCONTINUITY-SEQUENCE advances as
// a discontinuity slides out of the window.

// upSeg is one segment of a slot's playlist.
type upSeg struct {
	name   string
	dur    float64
	pdt    time.Time
	hasPDT bool
	disc   bool
}

// upPlaylist is a slot's media playlist.
type upPlaylist struct {
	target  int
	seq     int64
	version int
	segs    []upSeg
}

// parseMediaPlaylist reads an HLS media playlist (what ffmpeg's HLS muxer
// writes, with the slot's dates).
func parseMediaPlaylist(b []byte) (upPlaylist, error) {
	var pl upPlaylist
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var cur upSeg
	haveInf := false
	sawHeader := false
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch {
		case l == "":
		case l == "#EXTM3U":
			sawHeader = true
		case strings.HasPrefix(l, "#EXT-X-TARGETDURATION:"):
			pl.target, _ = strconv.Atoi(strings.TrimPrefix(l, "#EXT-X-TARGETDURATION:"))
		case strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"):
			pl.seq, _ = strconv.ParseInt(strings.TrimPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(l, "#EXT-X-VERSION:"):
			pl.version, _ = strconv.Atoi(strings.TrimPrefix(l, "#EXT-X-VERSION:"))
		case l == "#EXT-X-DISCONTINUITY":
			cur.disc = true
		case strings.HasPrefix(l, pdtTag):
			if t, ok := parsePDT(strings.TrimPrefix(l, pdtTag)); ok {
				cur.pdt, cur.hasPDT = t, true
			}
		case strings.HasPrefix(l, "#EXTINF:"):
			v := strings.TrimPrefix(l, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			cur.dur, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
			haveInf = true
		case strings.HasPrefix(l, "#"):
		default:
			if haveInf {
				cur.name = l
				pl.segs = append(pl.segs, cur)
			}
			cur, haveInf = upSeg{}, false
		}
	}
	if !sawHeader {
		return pl, fmt.Errorf("frontdoor: not an HLS playlist")
	}
	return pl, nil
}

const pdtTag = "#EXT-X-PROGRAM-DATE-TIME:"

func parsePDT(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z0700", "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// stEntry is one segment of the stitched playlist.
type stEntry struct {
	slot string
	upSeg
}

func (e stEntry) end() time.Time {
	return e.pdt.Add(time.Duration(e.dur * float64(time.Second)))
}

// stitchTolerance: a new source's segment counts as starting at the previous
// end when it begins up to this much before it (aligned output: within a frame).
const stitchTolerance = 250 * time.Millisecond

// Stitcher is one channel's stitched playlist.
type Stitcher struct {
	mu       sync.Mutex
	src      string
	entries  []stEntry
	firstSeq int64
	discSeq  int64
	target   int
	window   int
	switches int
	// pendingSwitch: the new source has no segment past the old one's end yet.
	pendingSwitch bool
	// a planned switch (story 33.7): segments of the current source are listed
	// only up to the one ending at cutAt; the first segment of planTo starting
	// where that one ends follows after one discontinuity
	planTo  string
	cutAt   time.Time
	reached bool
	ended   bool
}

// Plan arranges a switch to source to at the instant at (a swap or rollback):
// the current source's segments are listed while they end by at; the segment
// that contains at (or starts after it) comes from to. A plan that is already
// in place is kept.
func (st *Stitcher) Plan(to string, at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if to == st.src || (st.planTo == to && st.cutAt.Equal(at)) {
		return
	}
	st.planTo, st.cutAt, st.reached = to, at, false
}

// Reached reports whether the planned switch's instant is in the current
// source's playlist (the next segment must come from the plan's source).
func (st *Stitcher) Reached() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.planTo != "" && st.reached
}

// End marks the channel ended (removed from the lineup): the playlist gets
// EXT-X-ENDLIST.
func (st *Stitcher) End() {
	st.mu.Lock()
	st.ended = true
	st.mu.Unlock()
}

// Source is the slot the stitcher reads now.
func (st *Stitcher) Source() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.src
}

// Switches is how many source changes were stitched.
func (st *Stitcher) Switches() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.switches
}

// Update merges a slot's current playlist into the stitched one.
func (st *Stitcher) Update(src string, pl upPlaylist) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if pl.target > st.target {
		st.target = pl.target
	}
	if n := len(pl.segs); n > st.window {
		st.window = n
	}
	if len(st.entries) == 0 {
		// the first source: its own numbering, so a single slot reads as before
		st.src = src
		st.firstSeq = pl.seq
		for _, s := range pl.segs {
			st.entries = append(st.entries, stEntry{slot: src, upSeg: s})
		}
		st.trim()
		return
	}
	last := st.entries[len(st.entries)-1]
	if src != st.src || st.pendingSwitch {
		st.switchTo(src, pl, last)
		if st.src == src && !st.pendingSwitch && src == st.planTo {
			st.planTo, st.reached = "", false
		}
		st.trim()
		return
	}
	// same source: append what follows the last segment listed
	idx := -1
	for i, s := range pl.segs {
		if s.name == last.name {
			idx = i
		}
	}
	var add []upSeg
	if idx >= 0 {
		add = pl.segs[idx+1:]
	} else {
		// the last one listed already slid out upstream (no poll for a while):
		// whatever starts after its end; a gap is a discontinuity
		for _, s := range pl.segs {
			if !s.hasPDT || !last.hasPDT || !s.pdt.Before(last.end().Add(-stitchTolerance)) {
				add = append(add, s)
			}
		}
		if len(add) > 0 {
			add[0].disc = true
		}
	}
	for _, s := range add {
		if st.planTo != "" && s.hasPDT {
			end := s.pdt.Add(time.Duration(s.dur * float64(time.Second)))
			if end.After(st.cutAt.Add(stitchTolerance)) {
				st.reached = true // this one (and what follows) comes from the plan's source
				break
			}
		}
		st.entries = append(st.entries, stEntry{slot: src, upSeg: s})
	}
	st.trim()
}

// switchTo continues the playlist from another slot after last.
func (st *Stitcher) switchTo(src string, pl upPlaylist, last stEntry) {
	start := -1
	for i, s := range pl.segs {
		if !s.hasPDT || !last.hasPDT {
			continue
		}
		if !s.pdt.Before(last.end().Add(-stitchTolerance)) {
			start = i
			// no segment of the new source starts where the last one ended (it
			// cuts its segments elsewhere: a station started off the grid): the
			// one that holds that instant overlaps by less than a segment, which
			// a player plays through, instead of skipping one
			if i > 0 && pl.segs[i].pdt.After(last.end().Add(stitchTolerance)) {
				if p := pl.segs[i-1]; p.hasPDT && p.pdt.Add(time.Duration(p.dur*float64(time.Second))).After(last.end()) {
					start = i - 1
				}
			}
			break
		}
	}
	if start < 0 {
		if len(pl.segs) > 0 && (!pl.segs[len(pl.segs)-1].hasPDT || !last.hasPDT) {
			start = len(pl.segs) - 1 // no dates to align on: the newest segment
		} else {
			// the new slot has nothing past the old end yet: keep the list, retry
			st.src, st.pendingSwitch = src, true
			return
		}
	}
	add := pl.segs[start:]
	for i, s := range add {
		if i == 0 {
			s.disc = true
		}
		st.entries = append(st.entries, stEntry{slot: src, upSeg: s})
	}
	st.src, st.pendingSwitch = src, false
	st.switches++
}

// trim slides the window (the upstream list size).
func (st *Stitcher) trim() {
	w := st.window
	if w <= 0 {
		w = 10
	}
	for len(st.entries) > w {
		if st.entries[0].disc {
			st.discSeq++
		}
		st.entries = st.entries[1:]
		st.firstSeq++
	}
	// the first entry's own tag counts in the discontinuity sequence
	if len(st.entries) > 0 && st.entries[0].disc {
		st.entries[0].disc = false
		st.discSeq++
	}
}

// Render is the stitched playlist; segment URIs are s/{slot}/{name} (relative
// to the channel's playlist URL).
func (st *Stitcher) Render() []byte {
	st.mu.Lock()
	defer st.mu.Unlock()
	target := st.target
	for _, e := range st.entries {
		if c := int(math.Ceil(e.dur)); c > target {
			target = c
		}
	}
	if target <= 0 {
		target = 6
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", target)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", st.firstSeq)
	if st.discSeq > 0 {
		fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", st.discSeq)
	}
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, e := range st.entries {
		if e.disc {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		if e.hasPDT {
			b.WriteString(pdtTag + e.pdt.UTC().Format("2006-01-02T15:04:05.000Z") + "\n")
		}
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n", e.dur)
		b.WriteString("s/" + e.slot + "/" + e.name + "\n")
	}
	if st.ended {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return []byte(b.String())
}

// Len is the number of segments listed.
func (st *Stitcher) Len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.entries)
}
