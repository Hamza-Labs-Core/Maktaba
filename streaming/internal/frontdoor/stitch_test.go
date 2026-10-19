package frontdoor

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// slotPlaylist renders a slot's playlist: n segments of dur seconds, numbered
// from first, dated from start.
func slotPlaylist(first int64, n int, start time.Time, dur float64, discFirst bool) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-INDEPENDENT-SEGMENTS\n",
		int(dur+0.999), first)
	for i := 0; i < n; i++ {
		if i == 0 && discFirst {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		at := start.Add(time.Duration(float64(i) * dur * float64(time.Second)))
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n#EXT-X-PROGRAM-DATE-TIME:%s\nseg-%d.ts\n", dur,
			at.UTC().Format("2006-01-02T15:04:05.000Z"), first+int64(i))
	}
	return []byte(b.String())
}

func mustParse(t *testing.T, b []byte) upPlaylist {
	t.Helper()
	pl, err := parseMediaPlaylist(b)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

type rendered struct {
	seq, disc int64
	uris      []string
	discAt    []int
	pdts      []string
}

func render(t *testing.T, st *Stitcher) rendered {
	t.Helper()
	var r rendered
	pending := false
	for _, l := range strings.Split(string(st.Render()), "\n") {
		switch {
		case strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"):
			_, _ = fmt.Sscanf(l, "#EXT-X-MEDIA-SEQUENCE:%d", &r.seq)
		case strings.HasPrefix(l, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			_, _ = fmt.Sscanf(l, "#EXT-X-DISCONTINUITY-SEQUENCE:%d", &r.disc)
		case l == "#EXT-X-DISCONTINUITY":
			pending = true
		case strings.HasPrefix(l, pdtTag):
			r.pdts = append(r.pdts, strings.TrimPrefix(l, pdtTag))
		case l != "" && !strings.HasPrefix(l, "#"):
			if pending {
				r.discAt = append(r.discAt, len(r.uris))
				pending = false
			}
			r.uris = append(r.uris, l)
		}
	}
	return r
}

// A single slot reads as before: its own numbering and segments, under s/{slot}/.
func TestStitcherPassThrough(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	st := &Stitcher{}
	st.Update("blue", mustParse(t, slotPlaylist(1000, 5, t0, 2, false)))
	st.Update("blue", mustParse(t, slotPlaylist(1001, 5, t0.Add(2*time.Second), 2, false)))
	r := render(t, st)
	if r.seq != 1001 || len(r.uris) != 5 || r.uris[0] != "s/blue/seg-1001.ts" || r.uris[4] != "s/blue/seg-1005.ts" ||
		len(r.discAt) != 0 || r.disc != 0 {
		t.Fatalf("pass-through: %+v", r)
	}
}

// A switch to another slot continues at the segment starting where the last
// one listed ended, after one discontinuity; the media sequence keeps going and
// the discontinuity sequence advances as it slides out.
func TestStitcherSwitchAligned(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	st := &Stitcher{}
	st.Update("blue", mustParse(t, slotPlaylist(1000, 5, t0, 2, false))) // ends at t0+10 s
	// green started later: other numbers, the same boundaries, one more segment
	st.Update("green", mustParse(t, slotPlaylist(7000, 5, t0.Add(4*time.Second), 2, false)))
	r := render(t, st)
	want := []string{"s/blue/seg-1000.ts", "s/blue/seg-1001.ts", "s/blue/seg-1002.ts", "s/blue/seg-1003.ts",
		"s/blue/seg-1004.ts", "s/green/seg-7003.ts", "s/green/seg-7004.ts"}
	if strings.Join(r.uris[len(r.uris)-len(want)+2:], ",") != strings.Join(want[2:], ",") {
		t.Fatalf("after the switch: %v", r.uris)
	}
	if len(r.discAt) != 1 || r.uris[r.discAt[0]] != "s/green/seg-7003.ts" {
		t.Fatalf("discontinuity at %v in %v", r.discAt, r.uris)
	}
	if r.pdts[len(r.pdts)-2] != "2026-10-19T12:00:10.000Z" {
		t.Fatalf("the first green segment starts at %s", r.pdts[len(r.pdts)-2])
	}
	if r.seq != 1002 { // window of 5, two more entries
		t.Fatalf("media sequence %d", r.seq)
	}
	if st.Switches() != 1 || st.Source() != "green" {
		t.Fatalf("switches %d source %s", st.Switches(), st.Source())
	}
	// green goes on: the window slides; once the discontinuity slides out the
	// discontinuity sequence is 1
	for i := 1; i <= 6; i++ {
		st.Update("green", mustParse(t, slotPlaylist(7000+int64(i), 5, t0.Add(time.Duration(4+2*i)*time.Second), 2, false)))
	}
	r = render(t, st)
	if r.disc != 1 || len(r.discAt) != 0 || r.uris[0] != "s/green/seg-7006.ts" || r.seq != 1008 {
		t.Fatalf("after the slide: %+v", r)
	}
}

// The new slot has nothing past the old end yet: the playlist stays, and the
// switch completes on a later poll.
func TestStitcherPendingSwitch(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	st := &Stitcher{}
	st.Update("blue", mustParse(t, slotPlaylist(1, 5, t0, 2, false)))                      // to t0+10
	st.Update("green", mustParse(t, slotPlaylist(50, 3, t0.Add(2*time.Second), 2, false))) // to t0+8
	if r := render(t, st); len(r.uris) != 5 || len(r.discAt) != 0 {
		t.Fatalf("listed a green segment from before the end: %+v", r)
	}
	st.Update("green", mustParse(t, slotPlaylist(50, 5, t0.Add(2*time.Second), 2, false))) // to t0+12
	r := render(t, st)
	if r.uris[len(r.uris)-1] != "s/green/seg-54.ts" || len(r.discAt) != 1 || r.uris[r.discAt[0]] != "s/green/seg-54.ts" {
		t.Fatalf("pending switch: %+v", r)
	}
}

// A slot's own discontinuity (its muxer restarted) and a station that started
// over (other numbers) carry over.
func TestStitcherSameSlotRestart(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	st := &Stitcher{}
	st.Update("blue", mustParse(t, slotPlaylist(1, 3, t0, 2, false)))
	st.Update("blue", mustParse(t, slotPlaylist(900, 2, t0.Add(8*time.Second), 2, true)))
	r := render(t, st)
	// (the window is the slot's list size, 3)
	if len(r.uris) != 3 || r.uris[1] != "s/blue/seg-900.ts" || len(r.discAt) != 1 || r.discAt[0] != 1 {
		t.Fatalf("restart: %+v", r)
	}
}
