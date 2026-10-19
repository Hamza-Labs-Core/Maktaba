package verify

import (
	"bufio"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Results of a check (the API's verify_report uses the same words).
const (
	Pass    = "pass"
	Warn    = "warn"
	Fail    = "fail"
	Pending = "pending" // not measurable yet (no segment so far)
	Skip    = "skip"
)

// Result is one check of one channel.
type Result struct {
	Check  string `json:"check"`
	Result string `json:"result"`
	Value  string `json:"value,omitempty"`
}

// Seg is one segment of a media playlist.
type Seg struct {
	URI string    `json:"uri"`
	PDT time.Time `json:"pdt"`
	Dur float64   `json:"dur"`
	// FirstPTS of the segment's first video frame (sampled segments only).
	FirstPTS int64 `json:"first_pts,omitempty"`
	FirstIDR bool  `json:"first_idr,omitempty"`
	PSI      bool  `json:"psi,omitempty"` // the segment carries PAT and PMT
	Sampled  bool  `json:"sampled,omitempty"`
}

// End is the segment's end instant.
func (s Seg) End() time.Time { return s.PDT.Add(time.Duration(s.Dur * float64(time.Second))) }

// Playlist is a media playlist as the checks read it.
type Playlist struct {
	Target      int   `json:"target"`
	Sequence    int64 `json:"sequence"`
	Independent bool  `json:"independent"`
	AllPDT      bool  `json:"all_pdt"`
	Segs        []Seg `json:"segs"`
}

// ParsePlaylist reads an HLS media playlist.
func ParsePlaylist(b []byte) (Playlist, error) {
	var pl Playlist
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	header := false
	var cur Seg
	hasPDT, inf := false, false
	pl.AllPDT = true
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch {
		case l == "":
		case l == "#EXTM3U":
			header = true
		case l == "#EXT-X-INDEPENDENT-SEGMENTS":
			pl.Independent = true
		case strings.HasPrefix(l, "#EXT-X-TARGETDURATION:"):
			pl.Target, _ = strconv.Atoi(strings.TrimPrefix(l, "#EXT-X-TARGETDURATION:"))
		case strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"):
			pl.Sequence, _ = strconv.ParseInt(strings.TrimPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(l, "#EXT-X-PROGRAM-DATE-TIME:"):
			v := strings.TrimPrefix(l, "#EXT-X-PROGRAM-DATE-TIME:")
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z0700"} {
				if t, err := time.Parse(layout, v); err == nil {
					cur.PDT, hasPDT = t.UTC(), true
					break
				}
			}
		case strings.HasPrefix(l, "#EXTINF:"):
			v := strings.TrimPrefix(l, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			cur.Dur, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
			inf = true
		case strings.HasPrefix(l, "#"):
		default:
			if inf {
				cur.URI = l
				if !hasPDT {
					pl.AllPDT = false
				}
				pl.Segs = append(pl.Segs, cur)
			}
			cur, hasPDT, inf = Seg{}, false, false
		}
	}
	if !header {
		return pl, fmt.Errorf("verify: not an HLS playlist")
	}
	if len(pl.Segs) == 0 {
		pl.AllPDT = false
	}
	return pl, nil
}

// Sample is what the front door measured of one slot's output of a channel.
type Sample struct {
	Slot     string    `json:"slot"`
	At       time.Time `json:"at"`
	Status   int       `json:"status"` // the playlist's HTTP status (0: no answer)
	Error    string    `json:"error,omitempty"`
	Playlist Playlist  `json:"playlist"`
	// TS is the sampled HLS segments; Stream a stretch of the slot's live.ts
	// (the HDHomeRun path: PAT/PMT repetition, continuity, PCR across blocks).
	TS     TSReport `json:"ts"`
	Stream TSReport `json:"stream"`
}

// Have reports a playlist with at least one segment.
func (s *Sample) Have() bool { return s != nil && s.Status == 200 && len(s.Playlist.Segs) > 0 }

// Expect is what the release's output settings promise.
type Expect struct {
	Width, Height int
	FPS           int
	Segment       int // hls_time
	Keyint        int // the effective IDR grid
	PMTPID        int
	VideoPID      int
	AudioPID      int
	ServiceID     int
	AudioCodec    string // aac | ac3
}

func (e Expect) frame() float64 {
	if e.FPS <= 0 {
		return 1.0 / 30
	}
	return 1 / float64(e.FPS)
}

// Evaluate runs the per-channel stream checks of a staging sample (and, given
// the live slot's sample of the same channel, the splice checks).
func Evaluate(stg *Sample, live *Sample, exp Expect, now time.Time) []Result {
	var out []Result
	if !stg.Have() {
		why := "no segment yet"
		if stg != nil && stg.Error != "" {
			why = stg.Error
		} else if stg != nil && stg.Status != 0 && stg.Status != 200 {
			why = "the staging slot answered " + strconv.Itoa(stg.Status)
		}
		return []Result{{Check: "channel.first_segment", Result: Pending, Value: why}}
	}
	pl := stg.Playlist
	out = append(out, Result{Check: "channel.first_segment", Result: Pass,
		Value: fmt.Sprintf("%d segments listed", len(pl.Segs))})
	out = append(out, cadence(pl, exp))
	out = append(out, wallclock(pl, exp, now))
	out = append(out, hlsConformity(pl, exp))
	out = append(out, tsConformity(stg.TS, stg.Stream, exp))
	if live.Have() {
		out = append(out, spliceAlign(stg, live, exp))
		out = append(out, spliceCompat(stg.TS, live.TS))
	} else {
		out = append(out, Result{Check: "splice.compat", Result: Skip, Value: "not on air on the live slot"})
	}
	return out
}

func cadence(pl Playlist, exp Expect) Result {
	gop := float64(exp.Keyint)
	if gop <= 0 {
		gop = 2
	}
	seg := float64(exp.Segment)
	worst, gap := 0.0, 0.0
	for i, s := range pl.Segs {
		// the last segment of a block may be short: only the far side counts
		if d := s.Dur - seg; d > worst {
			worst = d
		}
		if i > 0 && pl.Segs[i-1].PDT.Year() > 1 && s.PDT.Year() > 1 {
			if g := s.PDT.Sub(pl.Segs[i-1].End()).Seconds(); g > gap {
				gap = g
			}
		}
	}
	v := fmt.Sprintf("longest segment %.3f s (hls_time %d s), largest gap %.3f s", seg+worst, exp.Segment, gap)
	switch {
	case gap > 2*seg:
		return Result{Check: "channel.cadence", Result: Fail, Value: v}
	case worst > gop+0.05 || gap > exp.frame()+0.01:
		return Result{Check: "channel.cadence", Result: Warn, Value: v}
	}
	return Result{Check: "channel.cadence", Result: Pass, Value: v}
}

func wallclock(pl Playlist, exp Expect, now time.Time) Result {
	last := pl.Segs[len(pl.Segs)-1]
	if last.PDT.Year() <= 1 {
		return Result{Check: "channel.wallclock", Result: Fail, Value: "no EXT-X-PROGRAM-DATE-TIME"}
	}
	d := last.End().Sub(now).Seconds()
	v := fmt.Sprintf("newest segment ends %+.3f s from now", d)
	if math.Abs(d) >= float64(exp.Segment)+1 {
		return Result{Check: "channel.wallclock", Result: Fail, Value: v}
	}
	return Result{Check: "channel.wallclock", Result: Pass, Value: v}
}

func hlsConformity(pl Playlist, _ Expect) Result {
	var bad []string
	if !pl.AllPDT {
		bad = append(bad, "segments without EXT-X-PROGRAM-DATE-TIME")
	}
	if !pl.Independent {
		bad = append(bad, "no EXT-X-INDEPENDENT-SEGMENTS")
	}
	longest := 0.0
	for _, s := range pl.Segs {
		longest = math.Max(longest, s.Dur)
	}
	if pl.Target <= 0 || float64(pl.Target) < math.Round(longest) {
		bad = append(bad, fmt.Sprintf("target duration %d below the longest segment %.3f s", pl.Target, longest))
	}
	for _, s := range pl.Segs {
		if s.Sampled && !s.FirstIDR {
			bad = append(bad, s.URI+" does not start with an IDR")
		}
		if s.Sampled && !s.PSI {
			bad = append(bad, s.URI+" carries no PAT/PMT")
		}
	}
	if len(bad) > 0 {
		return Result{Check: "hls.conformity", Result: Fail, Value: strings.Join(bad, "; ")}
	}
	return Result{Check: "hls.conformity", Result: Pass,
		Value: fmt.Sprintf("target %d s, program dates, independent segments, segments start with an IDR", pl.Target)}
}

// tsConformity checks the sampled segments (t) and, when sampled, a stretch of
// the continuous TS (st, live.ts: what an HDHomeRun client reads), where PAT/PMT
// must repeat every 0.5 s and continuity and PCR hold across segment cuts.
func tsConformity(t, st TSReport, exp Expect) Result {
	var bad []string
	if t.Packets == 0 {
		return Result{Check: "ts.conformity", Result: Pending, Value: "no TS sampled yet"}
	}
	if t.SyncLoss > 0 || st.SyncLoss > 0 {
		bad = append(bad, fmt.Sprintf("%d bytes out of sync", t.SyncLoss+st.SyncLoss))
	}
	if t.PATs == 0 || t.PMTs == 0 {
		bad = append(bad, "no PAT/PMT")
	}
	if st.Packets > 0 {
		switch {
		case st.PATs == 0 || st.PMTs == 0:
			bad = append(bad, "live.ts: no PAT/PMT")
		case st.MaxPATGap > 0.5:
			bad = append(bad, fmt.Sprintf("live.ts: PAT every %.2f s (> 0.5 s)", st.MaxPATGap))
		}
		if st.CCErrors > 0 {
			bad = append(bad, fmt.Sprintf("live.ts: %d continuity errors", st.CCErrors))
		}
		if st.PCRBack > 0 {
			bad = append(bad, fmt.Sprintf("live.ts: PCR went back %d times", st.PCRBack))
		}
		if exp.PMTPID != 0 && st.PMTPID != 0 && (st.PMTPID != exp.PMTPID || st.VideoPID != exp.VideoPID) {
			bad = append(bad, fmt.Sprintf("live.ts: PIDs pmt 0x%x video 0x%x", st.PMTPID, st.VideoPID))
		}
	}
	if exp.PMTPID != 0 && (t.PMTPID != exp.PMTPID || t.VideoPID != exp.VideoPID || t.AudioPID != exp.AudioPID) {
		bad = append(bad, fmt.Sprintf("PIDs pmt 0x%x video 0x%x audio 0x%x (want 0x%x 0x%x 0x%x)",
			t.PMTPID, t.VideoPID, t.AudioPID, exp.PMTPID, exp.VideoPID, exp.AudioPID))
	}
	if exp.ServiceID != 0 && t.ServiceID != exp.ServiceID {
		bad = append(bad, fmt.Sprintf("service id %d (want %d)", t.ServiceID, exp.ServiceID))
	}
	if t.Codec != "h264" {
		bad = append(bad, "video codec "+orNone(t.Codec)+" (want h264)")
	}
	if exp.Width > 0 && (t.Width != exp.Width || t.Height != exp.Height) {
		bad = append(bad, fmt.Sprintf("resolution %dx%d (want %dx%d)", t.Width, t.Height, exp.Width, exp.Height))
	}
	if exp.FPS > 0 && t.FPS > 0 && math.Abs(t.FPS-float64(exp.FPS)) > 0.5 {
		bad = append(bad, fmt.Sprintf("%.2f fps (want %d)", t.FPS, exp.FPS))
	}
	if exp.Keyint > 0 && t.MaxIDRGap > float64(exp.Keyint)+exp.frame()+0.01 {
		bad = append(bad, fmt.Sprintf("IDRs %.2f s apart (want every %d s)", t.MaxIDRGap, exp.Keyint))
	}
	if exp.Keyint > 0 && t.Duration >= float64(exp.Keyint)*2 && t.GridIDRs == 0 {
		bad = append(bad, fmt.Sprintf("no IDR on the %d s wall-clock grid", exp.Keyint))
	}
	if t.BFrames > 0 || t.PTSNotDTS > 0 {
		bad = append(bad, fmt.Sprintf("%d B-frames", maxI(t.BFrames, t.PTSNotDTS)))
	}
	if t.OpenGOP > 0 {
		bad = append(bad, fmt.Sprintf("%d open-GOP entry points", t.OpenGOP))
	}
	if t.CCErrors > 0 {
		bad = append(bad, fmt.Sprintf("%d continuity errors", t.CCErrors))
	}
	if t.PCRBack > 0 {
		bad = append(bad, fmt.Sprintf("PCR went back %d times", t.PCRBack))
	}
	if t.VideoPTSBad > 0 {
		bad = append(bad, fmt.Sprintf("video PTS went back %d times", t.VideoPTSBad))
	}
	if exp.AudioCodec != "" && t.AudioCodec != "" && t.AudioCodec != exp.AudioCodec {
		bad = append(bad, "audio "+t.AudioCodec+" (want "+exp.AudioCodec+")")
	}
	if len(bad) > 0 {
		return Result{Check: "ts.conformity", Result: Fail, Value: strings.Join(bad, "; ")}
	}
	v := fmt.Sprintf(
		"%.1f s: H.264 profile %d level %d %dx%d %.2f fps, IDR every %.2f s on the grid, no B-frames, CC 0, PCR monotonic, PIDs 0x%x/0x%x/0x%x",
		t.Duration, t.Profile, t.Level, t.Width, t.Height, t.FPS, t.MaxIDRGap, t.PMTPID, t.VideoPID, t.AudioPID)
	if st.Packets > 0 {
		v += fmt.Sprintf("; live.ts %.1f s, PAT every %.2f s", st.Duration, st.MaxPATGap)
	}
	return Result{Check: "ts.conformity", Result: Pass, Value: v}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// spliceAlign checks what makes the TS splice and the HLS stitch exact (story
// 33.4): the same PIDs, segment boundaries at the same instants and the same
// first PTS for a segment both slots list, within one frame.
func spliceAlign(stg, live *Sample, exp Expect) Result {
	frame := exp.frame()
	var bad []string
	if stg.TS.Packets > 0 && live.TS.Packets > 0 &&
		(stg.TS.PMTPID != live.TS.PMTPID || stg.TS.VideoPID != live.TS.VideoPID || stg.TS.AudioPID != live.TS.AudioPID) {
		bad = append(bad, fmt.Sprintf("PIDs 0x%x/0x%x/0x%x on staging, 0x%x/0x%x/0x%x on live",
			stg.TS.PMTPID, stg.TS.VideoPID, stg.TS.AudioPID, live.TS.PMTPID, live.TS.VideoPID, live.TS.AudioPID))
	}
	lp := live.Playlist.Segs
	if len(lp) == 0 {
		return Result{Check: "splice.align", Result: Pending, Value: "no live segments"}
	}
	from, to := lp[0].PDT, lp[len(lp)-1].End()
	common, worst := 0, 0.0
	var ptsDiff float64
	ptsSeen := false
	for _, s := range stg.Playlist.Segs {
		if s.PDT.Before(from) || s.PDT.After(to) {
			continue
		}
		best := math.Inf(1)
		var match Seg
		for _, l := range lp {
			if d := math.Abs(s.PDT.Sub(l.PDT).Seconds()); d < best {
				best, match = d, l
			}
		}
		common++
		if best > worst {
			worst = best
		}
		if s.Sampled && match.Sampled && best <= frame {
			d := math.Abs(float64(s.FirstPTS-match.FirstPTS)) / 90000
			if !ptsSeen || d > ptsDiff {
				ptsDiff, ptsSeen = d, true
			}
		}
	}
	if common == 0 {
		return Result{Check: "splice.align", Result: Pending, Value: "no segment in live's window yet"}
	}
	if worst > frame+0.0005 {
		bad = append(bad, fmt.Sprintf("segment boundaries %.0f ms off live's", worst*1000))
	}
	if ptsSeen && ptsDiff > frame+0.0005 {
		bad = append(bad, fmt.Sprintf("first PTS %.0f ms off live's", ptsDiff*1000))
	}
	if len(bad) > 0 {
		return Result{Check: "splice.align", Result: Fail, Value: strings.Join(bad, "; ")}
	}
	v := fmt.Sprintf("%d segment boundaries on live's (largest offset %.1f ms)", common, worst*1000)
	if ptsSeen {
		v += fmt.Sprintf(", first PTS %.1f ms apart", ptsDiff*1000)
	}
	return Result{Check: "splice.align", Result: Pass, Value: v}
}

// ParamChanges lists the stream parameters that differ between two samples.
func ParamChanges(stg, live TSReport) []string {
	var out []string
	add := func(name string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			out = append(out, fmt.Sprintf("%s %v → %v", name, b, a))
		}
	}
	add("codec", stg.Codec, live.Codec)
	add("profile", stg.Profile, live.Profile)
	add("level", stg.Level, live.Level)
	add("resolution", fmt.Sprintf("%dx%d", stg.Width, stg.Height), fmt.Sprintf("%dx%d", live.Width, live.Height))
	if math.Abs(stg.FPS-live.FPS) > 0.5 {
		add("fps", stg.FPS, live.FPS)
	}
	add("audio", stg.AudioCodec, live.AudioCodec)
	add("audio rate", stg.AudioRate, live.AudioRate)
	add("audio channels", stg.AudioChannels, live.AudioChannels)
	sort.Strings(out)
	return out
}

// spliceCompat: seamless when the parameters equal live's, else param_change
// (which ones) — the swap still works, the decoder restarts at the splice.
func spliceCompat(stg, live TSReport) Result {
	if stg.Packets == 0 || live.Packets == 0 {
		return Result{Check: "splice.compat", Result: Pending, Value: "no TS sampled yet"}
	}
	if ch := ParamChanges(stg, live); len(ch) > 0 {
		return Result{Check: "splice.compat", Result: Warn, Value: "param_change: " + strings.Join(ch, ", ")}
	}
	return Result{Check: "splice.compat", Result: Pass, Value: "seamless"}
}

// CloudflareIssues lists what a Cloudflare Stream live input would refuse or
// mishandle (design §6.2): H.264, AAC or AC-3 (the relay transcodes AC-3), a
// closed GOP of 2–10 s, combined bitrate 2–14 Mb/s, no B-frames (low latency).
// Issue strings starting with "b_frames" only matter with low latency on.
func CloudflareIssues(t TSReport) []string {
	var out []string
	if t.Packets == 0 {
		return []string{"no TS sampled"}
	}
	if t.Codec != "h264" {
		out = append(out, "video "+orNone(t.Codec)+" (H.264 required)")
	}
	if t.AudioCodec != "aac" && t.AudioCodec != "ac3" {
		out = append(out, "audio "+orNone(t.AudioCodec)+" (AAC required)")
	}
	if t.OpenGOP > 0 {
		out = append(out, "open GOP")
	}
	if t.MaxIDRGap > 0 && (t.MaxIDRGap < 2-0.05 || t.MaxIDRGap > 10+0.05) {
		out = append(out, fmt.Sprintf("keyframes every %.2f s (2–10 s)", t.MaxIDRGap))
	}
	if t.Bitrate > 0 && (t.Bitrate < 2e6 || t.Bitrate > 14e6) {
		out = append(out, fmt.Sprintf("bitrate %.2f Mb/s (2–14 Mb/s)", t.Bitrate/1e6))
	}
	if t.BFrames > 0 {
		out = append(out, fmt.Sprintf("b_frames: %d B-frames (low-latency HLS)", t.BFrames))
	}
	return out
}
