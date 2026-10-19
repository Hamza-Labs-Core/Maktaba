package channel

import (
	"context"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// --- story 33.4: splice-ready output --------------------------------------

// TC1: the offset of a block is its wall-clock start, so two stations of the
// same channel started 37 s apart stamp the same instant with the same PTS.
func TestTSOffsetWallClock(t *testing.T) {
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, fps := range []int{24, 25, 30, 60} {
		q := FrameQuantum(fps)
		// q is whole frames, whole microseconds and whole 90 kHz ticks
		if q%time.Microsecond != 0 || (int64(q)*90000)%int64(time.Second) != 0 ||
			(int64(q)*int64(fps))%int64(time.Second) != 0 {
			t.Fatalf("fps %d: quantum %v is not whole frames/µs/ticks", fps, q)
		}
		a := AlignStart(base.Add(1234567*time.Microsecond), fps)
		b := AlignStart(base.Add(37*time.Second+999*time.Millisecond), fps)
		// a frame instant common to both: its PTS from either block's offset
		at := AlignStart(base.Add(60*time.Second), fps)
		pa := TSOffsetTicks(a) + WallTicks(at) - WallTicks(a)
		pb := TSOffsetTicks(b) + WallTicks(at) - WallTicks(b)
		if (pa-pb)%PTSWrapTicks != 0 {
			t.Fatalf("fps %d: the same instant has PTS %d and %d", fps, pa, pb)
		}
		// the offset ffmpeg gets comes back to the same ticks (µs round trip)
		sec, _ := strconv.ParseFloat(TSOffsetSeconds(TSOffsetTicks(a)), 64)
		us := int64(math.Round(sec * 1e6))
		if back := int64(math.Round(float64(us) * 0.09)); back != TSOffsetTicks(a) {
			t.Fatalf("fps %d: offset %d ticks → %s s → %d ticks", fps, TSOffsetTicks(a), TSOffsetSeconds(TSOffsetTicks(a)), back)
		}
	}
	// a station started in one slot and another one 37 s later: block args differ
	// only by the offset, which is each one's wall-clock start
	s1 := AlignStart(base, 30)
	s2 := AlignStart(base.Add(37*time.Second), 30)
	if d := TSOffsetTicks(s2) - TSOffsetTicks(s1); d != 37*90000 {
		t.Fatalf("offsets 37 s apart differ by %d ticks", d)
	}
}

// TC2: the keyframe expression puts IDRs on the wall-clock grid whatever the
// block's start, after a block boundary and after a muxer restart alike.
func TestForceKeyFramesGrid(t *testing.T) {
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		off  time.Duration
		k    int
		want float64
	}{
		{0, 2, 2},                          // on the grid: the next one k later
		{300 * time.Millisecond, 2, 1.7},   // 0.3 s past a grid point
		{1900 * time.Millisecond, 2, 0.1},  // just before the next
		{3100 * time.Millisecond, 4, 0.9},  // a 4 s grid (VideoToolbox: one GOP per segment)
		{59500 * time.Millisecond, 2, 0.5}, // a minute in
	} {
		st := AlignStart(base.Add(c.off), 30)
		if got := KeyframePhase(st, c.k, 30); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("start +%v grid %d: phase %.4f, want %.4f", c.off, c.k, got, c.want)
		}
	}
	expr := KeyframeExpr(1.7, 2, 30)
	if expr != "expr:gte(t,n_forced*2+(-0.316667))" {
		t.Fatalf("expr %q", expr)
	}
	// simulate the expression over the frames of a block: IDR instants are the
	// first frame, then exactly the grid points
	st := AlignStart(base.Add(300*time.Millisecond), 30)
	phase := KeyframePhase(st, 2, 30)
	lead := phase - 2 - 0.5/30
	n := 0
	var idrs []time.Duration
	for i := 0; i < 30*7; i++ {
		tt := float64(i) / 30
		if tt >= float64(n)*2+lead {
			n++
			idrs = append(idrs, time.Duration(i)*time.Second/30)
		}
	}
	for _, d := range idrs[1:] {
		wall := st.Add(d)
		if wall.Sub(wall.Truncate(2*time.Second)) > time.Millisecond {
			t.Fatalf("IDR at %v is off the 2 s grid (block start %v)", wall, st)
		}
	}
	if idrs[0] != 0 || len(idrs) != 4 {
		t.Fatalf("IDRs %v", idrs)
	}
	// the arguments carry it, with closed GOPs and no B-frames
	args := joined(BlockArgs(OutputProfile{SegmentSeconds: 4, KeyintSeconds: 2},
		BlockInput{Kind: "slate", DurationSec: 5, WallStart: st, TSOffsetTicks: TSOffsetTicks(st)}))
	for _, want := range []string{"-force_key_frames " + KeyframeExpr(phase, 2, 30), "-bf 0", "-flags +cgop",
		"-output_ts_offset " + TSOffsetSeconds(TSOffsetTicks(st)), "-mpegts_copyts 1"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	// a keyframe interval that does not divide the segment is lowered to a divisor
	if p := (OutputProfile{SegmentSeconds: 3, KeyintSeconds: 2}).normalised(); p.KeyintSeconds != 1 {
		t.Errorf("keyint for 3 s segments: %d", p.KeyintSeconds)
	}
}

// TC3 (arguments): the PMT PID, the elementary stream PIDs and the service id are
// fixed in the block's, the muxer's TS and the HLS segments' arguments.
func TestFixedPIDsArgs(t *testing.T) {
	block := joined(BlockArgs(DefaultProfile(), BlockInput{Kind: "slate", DurationSec: 2}))
	mux := joined(MuxerArgs(DefaultProfile(), "/tmp/x", false))
	for _, want := range []string{"-mpegts_pmt_start_pid 4096", "-mpegts_start_pid 256", "-mpegts_service_id 1"} {
		if !strings.Contains(block, want) || !strings.Contains(mux, want) {
			t.Errorf("missing %q:\nblock %s\nmux %s", want, block, mux)
		}
	}
	if !strings.Contains(mux, "-hls_segment_options mpegts_copyts=1:mpegts_pmt_start_pid=4096:mpegts_start_pid=256:mpegts_service_id=1") {
		t.Errorf("HLS segments' layout not fixed: %s", mux)
	}
}

// TC5: PTS wrap. The offset of a block just past a wrap keeps the PCR above
// zero (one wrap later: the same 33-bit value); PTSToWall and OnGrid accept
// timestamps on either side of a wrap, with a clock on the other side.
func TestPTSWrap(t *testing.T) {
	// the instant of a wrap: Unix ticks a multiple of 2^33
	wrapTicks := (WallTicks(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC))/PTSWrapTicks + 1) * PTSWrapTicks
	wrapAt := time.Unix(wrapTicks/90000, (wrapTicks%90000)*100000/9)
	just := AlignStart(wrapAt.Add(500*time.Millisecond), 30)
	off := TSOffsetTicks(just)
	if off < PTSWrapTicks || off%PTSWrapTicks != WallTicks(just)%PTSWrapTicks {
		t.Fatalf("offset just past a wrap: %d", off)
	}
	before := AlignStart(wrapAt.Add(-1500*time.Millisecond), 30)
	if TSOffsetTicks(before) >= PTSWrapTicks {
		t.Fatalf("offset before the wrap moved: %d", TSOffsetTicks(before))
	}
	// a frame 1 s before the wrap read with the clock 2 s after it, and back
	pts := (WallTicks(wrapAt) - 90000) % PTSWrapTicks
	got := PTSToWall(pts, wrapAt.Add(2*time.Second))
	if d := got.Sub(wrapAt.Add(-time.Second)); d < -20*time.Microsecond || d > 20*time.Microsecond { // a tick
		t.Fatalf("PTS before the wrap → %v (want %v)", got, wrapAt.Add(-time.Second))
	}
	pts = (WallTicks(wrapAt) + 2*90000) % PTSWrapTicks
	got = PTSToWall(pts, wrapAt.Add(-time.Second))
	if d := got.Sub(wrapAt.Add(2 * time.Second)); d < -20*time.Microsecond || d > 20*time.Microsecond {
		t.Fatalf("PTS after the wrap → %v", got)
	}
	// a 4 s grid point right after the wrap is found from its 33-bit PTS
	grid := (wrapTicks/(4*90000) + 1) * 4 * 90000
	if !OnGrid(grid%PTSWrapTicks, wrapAt.Add(-3*time.Second), 4) || OnGrid((grid+3000)%PTSWrapTicks, wrapAt, 4) {
		t.Fatal("OnGrid across the wrap")
	}
}

// TC4: two stations of the same channel, started in different "slots" seconds
// apart with real ffmpeg, produce segments starting at the same instants (to
// the frame), the same PTS for them, the same program dates (to the
// millisecond) and the same PIDs.
func TestTwoStationsAligned_RealFFmpeg(t *testing.T) {
	needFFmpeg(t)
	tmp := t.TempDir()
	clip := filepath.Join(tmp, "a.mp4")
	makeClip(t, clip, true, 40)
	now := time.Now()
	// a programme that began 7.3 s ago: the stations join it mid-block (a seek),
	// then a slate, then the programme again
	repo := &syncRepo{blocks: []ProgramBlock{
		{Seq: 1, Kind: KindProgram, VideoID: uuid.New(), Path: clip, StartAt: now.Add(-7300 * time.Millisecond),
			EndAt: now.Add(9 * time.Second), SourceDurationMS: 16300},
		{Seq: 2, Kind: KindSlate, StartAt: now.Add(9 * time.Second), EndAt: now.Add(14100 * time.Millisecond)},
		{Seq: 3, Kind: KindProgram, VideoID: uuid.New(), Path: clip, StartAt: now.Add(14100 * time.Millisecond),
			EndAt: now.Add(60 * time.Second), SourceDurationMS: 40000},
	}}
	prof := OutputProfile{Width: 640, Height: 360, FPS: 30, VideoKbps: 1200, AudioCodec: "aac",
		AudioKbps: 96, Encoder: EncoderSoftware, SegmentSeconds: 2, KeyintSeconds: 2, HLSListSize: 60}
	ch := uuid.New()
	start := func(name string) *Station {
		st, err := StartStation(StationConfig{
			FFmpeg: "ffmpeg", Profile: prof, Repo: repo, Prober: NewFFprobe("ffprobe"),
			Resolver: NewYtDlp(""), PathMap: pathmap.Parse(""), FontFile: FindFont(), AlignSegments: true,
			Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		}, ch, filepath.Join(tmp, name))
		if err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
		t.Cleanup(func() { st.Stop(); <-st.Done() })
		return st
	}
	blue := start("blue")
	time.Sleep(3700 * time.Millisecond) // the second slot starts later
	green := start("green")
	for _, st := range []*Station{blue, green} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := st.WaitReady(ctx); err != nil {
			cancel()
			t.Fatalf("first segment: %v", err)
		}
		cancel()
	}
	time.Sleep(20 * time.Second) // across the slate and back

	type seg struct {
		name string
		pts  int64
		pdt  string
		dur  string
	}
	h := &HTTPHandler{}
	read := func(st *Station) []seg {
		b, err := readShared(filepath.Join(st.Dir(), "index.m3u8"))
		if err != nil {
			t.Fatal(err)
		}
		b = h.pdt.rewritePDT(st.Dir(), b, time.Now())
		var out []seg
		var cur seg
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			switch {
			case strings.HasPrefix(l, pdtTag):
				cur.pdt = strings.TrimPrefix(l, pdtTag)
			case strings.HasPrefix(l, "#EXTINF:"):
				cur.dur = strings.TrimSuffix(strings.TrimPrefix(l, "#EXTINF:"), ",")
			case strings.HasPrefix(l, "seg-"):
				cur.name = l
				cur.pts, _ = segmentFirstPTS(filepath.Join(st.Dir(), l))
				out = append(out, cur)
				cur = seg{}
			}
		}
		return out
	}
	a, b := read(blue), read(green)
	if len(a) < 8 || len(b) < 6 {
		t.Fatalf("segments: blue %d, green %d", len(a), len(b))
	}
	byPTS := map[int64]seg{}
	var lastBlue int64
	for _, s := range a {
		byPTS[s.pts] = s
		if s.pts > lastBlue {
			lastBlue = s.pts
		}
	}
	matched, maxDiff := 0, int64(0)
	for _, s := range b { // every green segment, the first one too (its muxer started on the grid)
		if s.pts > lastBlue {
			continue // blue's playlist was read a moment earlier
		}
		best := int64(math.MaxInt64)
		var m seg
		for p, o := range byPTS {
			d := p - s.pts
			if d < 0 {
				d = -d
			}
			if d < best {
				best, m = d, o
			}
		}
		if best > 3000 { // one frame at 30 fps
			t.Errorf("green segment %s (pts %d, %s) has no blue segment within a frame (nearest %d ticks)", s.name, s.pts, s.pdt, best)
			continue
		}
		if best > maxDiff {
			maxDiff = best
		}
		if m.pdt != s.pdt {
			t.Errorf("program dates differ: blue %s %s, green %s %s", m.name, m.pdt, s.name, s.pdt)
		}
		matched++
	}
	if matched < 5 {
		t.Fatalf("only %d green segments matched blue's", matched)
	}
	t.Logf("%d segments line up across the two stations; largest PTS difference %d ticks (%.2f ms); first green segment starts %s",
		matched, maxDiff, float64(maxDiff)/90, b[0].pdt)
	// segment boundaries on the wall-clock segment grid
	for _, s := range b {
		if !OnGrid(s.pts, time.Now(), prof.SegmentSeconds) {
			t.Errorf("green segment %s starts at %s, off the %d s grid", s.name, s.pdt, prof.SegmentSeconds)
		}
	}
	// TC3: the same stream layout in both (ffprobe)
	pids := func(dir, name string) string {
		out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=id,codec_name:program=program_id,pmt_pid",
			"-of", "compact", filepath.Join(dir, name)).Output()
		if err != nil {
			t.Fatalf("ffprobe: %v", err)
		}
		return string(out)
	}
	pa, pb := pids(blue.Dir(), a[len(a)-2].name), pids(green.Dir(), b[len(b)-2].name)
	if pa != pb || !strings.Contains(pa, "pmt_pid=4096") || !strings.Contains(pa, "id=0x100") || !strings.Contains(pa, "id=0x101") {
		t.Fatalf("stream layouts differ or are not fixed:\nblue  %s\ngreen %s", pa, pb)
	}
}

// The playlist's program dates come from each segment's first video PTS.
func TestRewritePDTFromPTS(t *testing.T) {
	dir := t.TempDir()
	at := AlignStart(time.Now().Add(-6*time.Second), 30)
	ticks := WallTicks(at) % PTSWrapTicks
	pkt := make([]byte, tsPacket)
	pkt[0], pkt[1], pkt[2], pkt[3] = 0x47, 0x40|byte(TSVideoPID>>8), byte(TSVideoPID&0xff), 0x10
	pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5,
		0x21 | byte(ticks>>29)&0x0e, byte(ticks >> 22), byte(ticks>>14) | 1, byte(ticks >> 7), byte(ticks<<1) | 1,
		0, 0, 0, 1, 0x65}
	copy(pkt[4:], pes)
	if _, ok := tsVideoKeyframe(pkt); !ok {
		t.Fatal("keyframe not seen")
	}
	if err := os.WriteFile(filepath.Join(dir, "seg-7.ts"), pkt, 0o644); err != nil {
		t.Fatal(err)
	}
	pl := "#EXTM3U\n#EXTINF:2.000000,\n#EXT-X-PROGRAM-DATE-TIME:2001-01-01T00:00:00.000+0000\nseg-7.ts\n" +
		"#EXTINF:2.000000,\n#EXT-X-PROGRAM-DATE-TIME:2001-01-01T00:00:02.000+0000\nseg-8.ts\n"
	var c pdtCache
	out := string(c.rewritePDT(dir, []byte(pl), time.Now()))
	want := pdtTag + at.UTC().Format(pdtLayout)
	if !strings.Contains(out, want) || !strings.Contains(out, "2001-01-01T00:00:02.000+0000") {
		t.Fatalf("rewritten:\n%s\nwant %s", out, want)
	}
	if _, ok := c.firstPTS(filepath.Join(dir, "seg-7.ts")); !ok {
		t.Fatal("not cached")
	}
	c.rewritePDT(dir, []byte("#EXTM3U\n"), time.Now()) // nothing listed: pruned
	if len(c.m) != 0 {
		t.Fatalf("cache %v", c.m)
	}
}
