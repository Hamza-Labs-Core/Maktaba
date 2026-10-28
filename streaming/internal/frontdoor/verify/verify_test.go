package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- crafted TS (no ffmpeg) ----

// tsw writes TS packets.
type tsw struct {
	b  []byte
	cc map[int]int
}

func newTSW() *tsw { return &tsw{cc: map[int]int{}} }

func (w *tsw) packet(pid int, pusi bool, af []byte, payload []byte) {
	p := make([]byte, 188)
	p[0] = 0x47
	p[1] = byte(pid >> 8 & 0x1f)
	if pusi {
		p[1] |= 0x40
	}
	p[2] = byte(pid)
	afc := byte(0x10)
	off := 4
	if af != nil {
		afc |= 0x20
		p[4] = byte(len(af))
		copy(p[5:], af)
		off = 5 + len(af)
	}
	p[3] = afc | byte(w.cc[pid]&0x0f)
	w.cc[pid]++
	n := copy(p[off:], payload)
	for i := off + n; i < 188; i++ {
		p[i] = 0xff
	}
	w.b = append(w.b, p...)
}

func psi(tableID byte, body []byte) []byte {
	l := len(body) + 4
	s := []byte{0, tableID, 0xb0 | byte(l>>8), byte(l)}
	s = append(s, body...)
	return append(s, 0, 0, 0, 0) // CRC (not checked)
}

func (w *tsw) pat(pmt int) {
	w.packet(0, true, nil, psi(0, []byte{0, 1, 0xc1, 0, 0, 0, 1, 0xe0 | byte(pmt>>8), byte(pmt)}))
}

func (w *tsw) pmt(pmt, video, audio int) {
	body := []byte{0, 1, 0xc1, 0, 0, 0xe0 | byte(video>>8), byte(video), 0xf0, 0,
		StreamH264, 0xe0 | byte(video>>8), byte(video), 0xf0, 0,
		StreamAAC, 0xe0 | byte(audio>>8), byte(audio), 0xf0, 0}
	w.packet(pmt, true, nil, psi(2, body))
}

func ptsBytes(v int64, marker byte) []byte {
	return []byte{marker<<4 | byte(v>>29&0x0e) | 1, byte(v >> 22), byte(v>>14&0xfe) | 1, byte(v >> 7), byte(v<<1) | 1}
}

// frame writes one video PES: an IDR (with an SPS-less slice) or a P slice.
func (w *tsw) frame(pid int, pts int64, idr bool, pcr bool) {
	pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5}
	pes = append(pes, ptsBytes(pts, 2)...)
	if idr {
		pes = append(pes, 0, 0, 0, 1, 0x65, 0x88) // IDR, first_mb 0 (1), slice_type 7 (I)... ue bits
	} else {
		pes = append(pes, 0, 0, 0, 1, 0x41, 0x9a) // P slice: first_mb 0, slice_type 5 (P)
	}
	var af []byte
	if pcr {
		base := pts - 9000
		af = []byte{0x10, byte(base >> 25), byte(base >> 17), byte(base >> 9), byte(base >> 1), byte(base<<7) | 0x7e, 0}
	}
	w.packet(pid, true, af, pes)
}

func craft(seconds int, fps int, opts func(w *tsw, i int) bool) []byte {
	w := newTSW()
	step := int64(90000 / fps)
	base := int64(90000 * 1000) // on the 2 s grid
	for i := 0; i < seconds*fps; i++ {
		if i%3 == 0 {
			w.pat(0x1000)
			w.pmt(0x1000, 0x100, 0x101)
		}
		if opts != nil && !opts(w, i) {
			continue
		}
		w.frame(0x100, base+int64(i)*step, i%(2*fps) == 0, true)
	}
	return w.b
}

func TestChecksTSConformityCrafted(t *testing.T) {
	exp := Expect{FPS: 30, Segment: 2, Keyint: 2, PMTPID: 0x1000, VideoPID: 0x100, AudioPID: 0x101, ServiceID: 1}
	good := AnalyzeTS(craft(6, 30, nil), Options{Grid: 2})
	if good.PATs == 0 || good.PMTPID != 0x1000 || good.VideoPID != 0x100 || good.AudioPID != 0x101 {
		t.Fatalf("layout: %+v", good)
	}
	if good.IDRs != 3 || good.GridIDRs != 3 || good.MaxIDRGap < 1.99 || good.MaxIDRGap > 2.01 {
		t.Fatalf("IDRs: %+v", good)
	}
	if good.FPS != 30 || good.CCErrors != 0 || good.PCRBack != 0 {
		t.Fatalf("fps/cc/pcr: %+v", good)
	}
	// the crafted slices carry no SPS: only the codec side may be off
	r := tsConformity(good, TSReport{}, exp)
	if r.Result != Pass {
		t.Fatalf("good layout: %+v", r)
	}

	// missing PAT
	noPAT := craft(4, 30, func(*tsw, int) bool { return true })
	var filtered []byte
	for i := 0; i+188 <= len(noPAT); i += 188 {
		p := noPAT[i : i+188]
		if int(p[1]&0x1f)<<8|int(p[2]) == 0 {
			continue
		}
		filtered = append(filtered, p...)
	}
	if r := tsConformity(AnalyzeTS(filtered, Options{Grid: 2}), TSReport{}, exp); r.Result != Fail || !strings.Contains(r.Value, "no PAT/PMT") {
		t.Errorf("missing PAT: %+v", r)
	}

	// continuity errors: skip a counter
	cc := craft(4, 30, func(w *tsw, i int) bool {
		if i == 40 {
			w.cc[0x100] += 3
		}
		return true
	})
	rep := AnalyzeTS(cc, Options{Grid: 2})
	if rep.CCErrors != 1 {
		t.Errorf("CC errors %d, want 1", rep.CCErrors)
	}
	if r := tsConformity(rep, TSReport{}, exp); r.Result != Fail || !strings.Contains(r.Value, "continuity") {
		t.Errorf("CC: %+v", r)
	}

	// wrong PIDs (a misbuilt release)
	w := newTSW()
	for i := 0; i < 90; i++ {
		if i%3 == 0 {
			w.pat(0x1100)
			w.pmt(0x1100, 0x200, 0x201)
		}
		w.frame(0x200, int64(90000*1000)+int64(i)*3000, i%60 == 0, true)
	}
	if r := tsConformity(AnalyzeTS(w.b, Options{Grid: 2}), TSReport{}, exp); r.Result != Fail || !strings.Contains(r.Value, "PIDs pmt 0x1100") {
		t.Errorf("PIDs: %+v", r)
	}

	// PCR going back
	back := craft(3, 30, nil)
	rep = AnalyzeTS(append(back, craft(1, 30, nil)...), Options{Grid: 2})
	if rep.PCRBack == 0 {
		t.Errorf("PCR back not seen: %+v", rep)
	}
}

// PSIScan finds the first PAT and then its PMT however the bytes are cut, and a PMT
// before any PAT does not count; PCRSpan measures a read with no PAT at all.
func TestPSIScanAndPCRSpan(t *testing.T) {
	w := newTSW()
	w.pmt(0x1000, 0x100, 0x101) // a PMT before the first PAT (a read joining mid-stream)
	w.frame(0x100, 90000*1000, true, true)
	w.pat(0x1000)
	w.frame(0x100, 90000*1000+3000, false, true)
	w.pmt(0x1000, 0x100, 0x101)
	b := w.b
	var p PSIScan
	for i := 0; i < 4*188; i += 7 { // the PMT, a frame, the PAT, a frame: not yet
		end := min(i+7, 4*188)
		if p.Feed(b[i:end]) {
			t.Fatalf("PSI seen at byte %d (a PMT before the PAT counted)", end)
		}
	}
	if !p.PAT || p.PMT {
		t.Fatalf("after the PAT: %+v", p)
	}
	if !p.Feed(b[4*188:]) {
		t.Fatal("the PMT after the PAT was not seen")
	}

	all := craft(3, 30, nil)
	var video []byte
	for i := 0; i+188 <= len(all); i += 188 {
		if pid := int(all[i+1]&0x1f)<<8 | int(all[i+2]); pid == 0x100 {
			video = append(video, all[i:i+188]...)
		}
	}
	r := AnalyzeTS(video, Options{})
	if r.PATs != 0 || r.PCRSpan < 2.9 || r.PCRSpan > 3.0 || !LiveTSJudged(r) {
		t.Fatalf("a 3 s read without PAT: pats %d, pcr span %.3f", r.PATs, r.PCRSpan)
	}
	if short := AnalyzeTS(video[:10*188], Options{}); short.PCRSpan > 0.5 || LiveTSJudged(short) {
		t.Fatalf("ten frames: pcr span %.3f judged %v", short.PCRSpan, LiveTSJudged(short))
	}
}

// ---- ffmpeg-made TS ----

func ffmpegOr(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		if os.Getenv("MAKTABA_REQUIRE_FFMPEG") == "1" {
			t.Fatal("ffmpeg not on PATH")
		}
		t.Skip("ffmpeg not on PATH")
	}
	return bin
}

func encode(t *testing.T, ff string, extra ...string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "s.ts")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=854x480:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "6",
		"-c:v", "libx264", "-preset", "ultrafast", "-profile:v", "high", "-pix_fmt", "yuv420p"}
	args = append(args, extra...)
	args = append(args, "-c:a", "aac", "-b:a", "64k", "-ac", "2", "-mpegts_pmt_start_pid", "4096",
		"-mpegts_start_pid", "256", "-mpegts_service_id", "1", "-output_ts_offset", "1000", "-mpegts_copyts", "1", "-f", "mpegts", out)
	if b, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestChecksTSConformity(t *testing.T) {
	ff := ffmpegOr(t)
	exp := Expect{Width: 854, Height: 480, FPS: 30, Segment: 2, Keyint: 2, PMTPID: 0x1000, VideoPID: 0x100,
		AudioPID: 0x101, ServiceID: 1, AudioCodec: "aac"}
	good := encode(t, ff, "-bf", "0", "-g", "60", "-keyint_min", "60", "-sc_threshold", "0", "-flags", "+cgop",
		"-force_key_frames", "expr:gte(t,n_forced*2)", "-b:v", "3000k")
	rep := AnalyzeTS(good, Options{Grid: 2})
	if rep.Codec != "h264" || rep.Width != 854 || rep.Height != 480 || rep.Profile == 0 || rep.Level == 0 || rep.FPS != 30 || rep.GridIDRs < 3 {
		t.Fatalf("parameters: %+v", rep)
	}
	if rep.AudioCodec != "aac" || rep.AudioRate != 48000 || rep.AudioChannels != 2 {
		t.Fatalf("audio: %+v", rep)
	}
	if rep.BFrames != 0 || rep.IDRs < 3 || rep.MaxIDRGap > 2.05 || rep.CCErrors != 0 || !rep.FirstIDR {
		t.Fatalf("GOP: %+v", rep)
	}
	if r := tsConformity(rep, TSReport{}, exp); r.Result != Pass {
		t.Fatalf("good stream: %+v", r)
	}
	if is := CloudflareIssues(rep); len(is) != 0 {
		t.Errorf("good stream for Cloudflare: %v", is)
	}

	// B-frames (and the longer GOP) fail
	bf := AnalyzeTS(encode(t, ff, "-bf", "2", "-g", "150", "-b:v", "3000k"), Options{Grid: 2})
	if bf.BFrames == 0 {
		t.Fatalf("no B-frames seen: %+v", bf)
	}
	if r := tsConformity(bf, TSReport{}, exp); r.Result != Fail || !strings.Contains(r.Value, "B-frames") || !strings.Contains(r.Value, "IDRs") {
		t.Errorf("B-frames: %+v", r)
	}

	// wrong fps / resolution
	odd := encode(t, ff, "-bf", "0", "-g", "50", "-vf", "fps=25,scale=640:360", "-b:v", "3000k")
	if r := tsConformity(AnalyzeTS(odd, Options{Grid: 2}), TSReport{}, exp); r.Result != Fail ||
		!strings.Contains(r.Value, "25.00 fps") || !strings.Contains(r.Value, "640x360") {
		t.Errorf("fps/resolution: %+v", r)
	}
	// splice.compat between them
	if ch := ParamChanges(AnalyzeTS(odd, Options{}), rep); len(ch) < 2 {
		t.Errorf("param changes: %v", ch)
	}
}

func TestCloudflareIngestRules(t *testing.T) {
	ok := TSReport{Packets: 1, Codec: "h264", AudioCodec: "aac", MaxIDRGap: 2, Bitrate: 6e6}
	cases := []struct {
		name string
		mod  func(r *TSReport)
		want string
	}{
		{"good", func(*TSReport) {}, ""},
		{"ac3 is transcoded by the relay", func(r *TSReport) { r.AudioCodec = "ac3" }, ""},
		{"hevc", func(r *TSReport) { r.Codec = "hevc" }, "H.264 required"},
		{"mp2 audio", func(r *TSReport) { r.AudioCodec = "mp2" }, "AAC required"},
		{"gop 1 s", func(r *TSReport) { r.MaxIDRGap = 1 }, "keyframes every 1.00 s"},
		{"gop 12 s", func(r *TSReport) { r.MaxIDRGap = 12 }, "keyframes every 12.00 s"},
		{"low bitrate", func(r *TSReport) { r.Bitrate = 0.9e6 }, "bitrate 0.90 Mb/s"},
		{"high bitrate", func(r *TSReport) { r.Bitrate = 20e6 }, "bitrate 20.00 Mb/s"},
		{"open gop", func(r *TSReport) { r.OpenGOP = 2 }, "open GOP"},
		{"b-frames", func(r *TSReport) { r.BFrames = 4 }, "b_frames"},
	}
	for _, c := range cases {
		r := ok
		c.mod(&r)
		got := strings.Join(CloudflareIssues(r), "; ")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func seg(uri string, pdt time.Time, dur float64, pts int64) Seg {
	return Seg{URI: uri, PDT: pdt, Dur: dur, FirstPTS: pts, Sampled: true, FirstIDR: true}
}

func TestSpliceCompat(t *testing.T) {
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	exp := Expect{FPS: 30, Segment: 2, Keyint: 2}
	tsr := TSReport{Packets: 10, Codec: "h264", Profile: 100, Level: 31, Width: 854, Height: 480, FPS: 30,
		AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2, PMTPID: 0x1000, VideoPID: 0x100, AudioPID: 0x101}
	mk := func(shift time.Duration, ptsShift int64) *Sample {
		s := &Sample{Status: 200, TS: tsr}
		for i := 0; i < 4; i++ {
			at := base.Add(time.Duration(i)*2*time.Second + shift)
			s.Playlist.Segs = append(s.Playlist.Segs, seg("seg-"+string(rune('a'+i))+".ts", at, 2, int64(90000*(1000+2*i))+ptsShift))
		}
		return s
	}
	live := mk(0, 0)
	if r := spliceAlign(mk(0, 0), live, exp); r.Result != Pass {
		t.Errorf("aligned: %+v", r)
	}
	if r := spliceCompat(tsr, tsr); r.Result != Pass || r.Value != "seamless" {
		t.Errorf("equal params: %+v", r)
	}
	if r := spliceAlign(mk(700*time.Millisecond, 63000), live, exp); r.Result != Fail || !strings.Contains(r.Value, "700 ms off") {
		t.Errorf("shifted boundaries: %+v", r)
	}
	if r := spliceAlign(mk(0, 9000), live, exp); r.Result != Fail || !strings.Contains(r.Value, "first PTS 100 ms off") {
		t.Errorf("shifted PTS: %+v", r)
	}
	pids := mk(0, 0)
	pids.TS.PMTPID, pids.TS.VideoPID, pids.TS.AudioPID = 0x1100, 0x200, 0x201
	if r := spliceAlign(pids, live, exp); r.Result != Fail || !strings.Contains(r.Value, "PIDs 0x1100/0x200/0x201 on staging") {
		t.Errorf("PIDs: %+v", r)
	}
	hd := tsr
	hd.Width, hd.Height, hd.Profile = 1280, 720, 77
	if r := spliceCompat(hd, tsr); r.Result != Warn || !strings.Contains(r.Value, "param_change") ||
		!strings.Contains(r.Value, "resolution 854x480 → 1280x720") || !strings.Contains(r.Value, "profile 100 → 77") {
		t.Errorf("param change: %+v", r)
	}
}

func TestPlaylistChecks(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 10, 0, time.UTC)
	body := "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:5\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-19T12:00:04.000Z\n#EXTINF:2.000000,\nseg-5.ts\n" +
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-19T12:00:06.000Z\n#EXTINF:2.000000,\nseg-6.ts\n"
	pl, err := ParsePlaylist([]byte(body))
	if err != nil || len(pl.Segs) != 2 || !pl.Independent || !pl.AllPDT || pl.Target != 2 {
		t.Fatalf("parse: %+v %v", pl, err)
	}
	exp := Expect{FPS: 30, Segment: 2, Keyint: 2}
	if r := wallclock(pl, exp, now); r.Result != Pass {
		t.Errorf("wallclock: %+v", r)
	}
	if r := wallclock(pl, exp, now.Add(30*time.Second)); r.Result != Fail {
		t.Errorf("stale playlist: %+v", r)
	}
	if r := hlsConformity(pl, exp); r.Result != Pass {
		t.Errorf("hls: %+v", r)
	}
	if r := cadence(pl, exp); r.Result != Pass {
		t.Errorf("cadence: %+v", r)
	}
	bad, _ := ParsePlaylist([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\nseg-1.ts\n"))
	if r := hlsConformity(bad, exp); r.Result != Fail || !strings.Contains(r.Value, "INDEPENDENT") {
		t.Errorf("bad hls: %+v", r)
	}
	if rs := Evaluate(&Sample{Status: 404}, nil, exp, now); rs[0].Result != Pending {
		t.Errorf("no playlist: %+v", rs)
	}
}
