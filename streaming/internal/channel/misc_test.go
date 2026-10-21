package channel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestParseLook_DefaultsAndOverrides(t *testing.T) {
	d := ParseLook(nil, " /logos/kids.png ")
	if d.Watermark != nil || !d.Loudnorm.Enabled || d.Loudnorm.TargetLUFS != -16 || d.LogoPath != "/logos/kids.png" {
		t.Errorf("defaults: %+v", d)
	}
	l := ParseLook([]byte(`{"watermark":{"path":"/w.gif","position":"bottom-left","width_pct":15,"margin_pct":1},
		"loading_path":"/load.jpg","loudnorm":{"enabled":false,"target_lufs":-23}}`), "")
	if l.Watermark == nil || l.Watermark.Position != "bottom-left" || l.Watermark.WidthPct != 15 || l.Watermark.MarginPct != 1 {
		t.Errorf("watermark: %+v", l.Watermark)
	}
	if l.LoadingPath != "/load.jpg" || l.Loudnorm.Enabled || l.Loudnorm.TargetLUFS != -23 {
		t.Errorf("look: %+v", l)
	}
	p := ParseLook([]byte(`{"watermark":{"path":"/w.png","position":"middle"}}`), "")
	if p.Watermark.Position != DefaultWMPosition || p.Watermark.WidthPct != DefaultWMWidthPct {
		t.Errorf("partial watermark defaults: %+v", p.Watermark)
	}
	if g := ParseLook([]byte(`not json`), ""); !g.Loudnorm.Enabled {
		t.Error("garbage should give defaults")
	}
	if e := ParseLook([]byte(`{"watermark":{"path":""}}`), ""); e.Watermark != nil {
		t.Error("empty watermark path → none")
	}
	if d.WatermarkSkipFiller {
		t.Error("a look without watermark_skip_filler keeps the watermark on filler")
	}
	if k := ParseLook([]byte(`{"watermark_skip_filler":true}`), ""); !k.WatermarkSkipFiller {
		t.Error("watermark_skip_filler not read")
	}
}

// TestApplyLook_WatermarkSkipFiller: with the rule the watermark is left off
// filler and bumper blocks only; programmes and live broadcasts keep it,
// and loudness applies to every block.
func TestApplyLook_WatermarkSkipFiller(t *testing.T) {
	wm := filepath.Join(t.TempDir(), "bug.png")
	_ = os.WriteFile(wm, []byte("png"), 0o644)
	s := &Station{}
	for _, skip := range []bool{false, true} {
		look := DefaultLook()
		look.Watermark = &Watermark{Path: wm, Position: "top-right", WidthPct: 10, MarginPct: 3}
		look.WatermarkSkipFiller = skip
		for _, kind := range []string{KindProgram, KindFiller, KindBumper, KindLive} {
			in := &BlockInput{}
			s.applyLook(context.Background(), in, look, kind)
			want := !skip || kind == KindProgram || kind == KindLive
			if got := in.Watermark != nil; got != want {
				t.Errorf("skip=%v %s: watermark %v, want %v", skip, kind, got, want)
			}
			if in.Loudnorm == nil || !in.Loudnorm.Enabled {
				t.Errorf("skip=%v %s: loudnorm %+v", skip, kind, in.Loudnorm)
			}
		}
	}
}

func TestYtDlp_DirectVsPageAndCache(t *testing.T) {
	y := NewYtDlp("yt-dlp-test")
	calls := 0
	y.run = func(_ context.Context, bin string, args ...string) ([]byte, error) {
		calls++
		if bin != "yt-dlp-test" || args[0] != "-f" || args[1] != "best[height<=1080][protocol^=m3u8]/best" || args[2] != "-g" {
			t.Errorf("args: %s %v", bin, args)
		}
		return []byte("\nhttps://cdn.example/live/index.m3u8?sig=1\n"), nil
	}
	for _, direct := range []string{"https://x.example/live.m3u8", "rtmp://x/live", "http://x/a.ts?token=1"} {
		got, err := y.Resolve(context.Background(), direct)
		if err != nil || got != direct {
			t.Errorf("direct %q → %q %v", direct, got, err)
		}
	}
	page := "https://www.youtube.com/watch?v=abc"
	for i := 0; i < 3; i++ {
		got, err := y.Resolve(context.Background(), page)
		if err != nil || got != "https://cdn.example/live/index.m3u8?sig=1" {
			t.Fatalf("page → %q %v", got, err)
		}
	}
	if calls != 1 {
		t.Errorf("yt-dlp ran %d times, want 1 (cached)", calls)
	}
	y.Forget(page)
	_, _ = y.Resolve(context.Background(), page)
	if calls != 2 {
		t.Errorf("Forget should force a re-resolve")
	}
	y.run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("offline") }
	if _, err := y.Resolve(context.Background(), "https://twitch.tv/x"); err == nil {
		t.Error("yt-dlp failure must surface")
	}
}

func TestPacketWriter_OnlyWholePackets(t *testing.T) {
	var out bytes.Buffer
	pw := &packetWriter{w: &out}
	pkt := bytes.Repeat([]byte{0x47}, tsPacket)
	data := append(append(append([]byte{}, pkt...), pkt...), pkt[:100]...)
	_, _ = pw.Write(data[:50])
	_, _ = pw.Write(data[50:300])
	_, _ = pw.Write(data[300:])
	if out.Len() != 2*tsPacket {
		t.Fatalf("forwarded %d bytes, want %d", out.Len(), 2*tsPacket)
	}
	pw.Discard() // block exited mid-packet
	_, _ = pw.Write(pkt)
	if out.Len() != 3*tsPacket || out.Bytes()[2*tsPacket] != 0x47 {
		t.Errorf("next block must start aligned")
	}
}

func TestHub_FanOutAndSlowClientDrop(t *testing.T) {
	h := NewHub(2)
	fast, cancelFast := h.Subscribe()
	slow, _ := h.Subscribe()
	if h.Clients() != 2 {
		t.Fatal("clients")
	}
	chunk := bytes.Repeat([]byte{0x47}, tsPacket)
	h.Publish(chunk)
	<-fast
	h.Publish(chunk)
	<-fast
	h.Publish(chunk) // slow has 2 queued → dropped
	<-fast
	if h.Clients() != 1 {
		t.Errorf("slow client should be dropped, clients=%d", h.Clients())
	}
	n := 0
	for range slow {
		n++
	}
	if n != 2 {
		t.Errorf("slow client drained %d", n)
	}
	cancelFast()
	h.Close()
	if _, ok := <-fast; ok {
		t.Error("closed")
	}
}

func TestParseStreamTypes(t *testing.T) {
	if i := parseStreamTypes("video\naudio\nsubtitle\n"); !i.HasVideo || !i.HasAudio {
		t.Error(i)
	}
	if i := parseStreamTypes("video,\r\n"); !i.HasVideo || i.HasAudio {
		t.Error(i)
	}
}

func TestProfileFromEnv(t *testing.T) {
	t.Setenv("MAKTABA_CHANNEL_RESOLUTION", "1280x721")
	t.Setenv("MAKTABA_CHANNEL_AUDIO_CODEC", "AC3")
	t.Setenv("MAKTABA_CHANNEL_ENCODER", "software")
	p, enc := ProfileFromEnv()
	if p.Width != 1280 || p.Height != 720 || p.AudioCodec != "ac3" || enc != "software" || p.FPS != 30 {
		t.Errorf("%+v %s", p, enc)
	}
	if DetectEncoder(context.Background(), "ffmpeg", "software") != EncoderSoftware ||
		DetectEncoder(context.Background(), "ffmpeg", "nvenc") != EncoderNVENC {
		t.Error("explicit encoder preference ignored")
	}
}

func pesPacket(pts int64) []byte {
	p := make([]byte, tsPacket)
	p[0], p[1], p[2], p[3] = 0x47, 0x40|0x01, 0x00, 0x10 // PUSI, pid 0x100, payload only
	h := p[4:]
	h[0], h[1], h[2], h[3] = 0, 0, 1, 0xE0
	h[6], h[7], h[8] = 0x80, 0x80, 5
	h[9] = byte(0x21 | (pts>>29)&0x0E)
	h[10] = byte(pts >> 22)
	h[11] = byte((pts>>14)&0xFE | 1)
	h[12] = byte(pts >> 7)
	h[13] = byte((pts<<1)&0xFE | 1)
	return p
}

func TestPTSTracker(t *testing.T) {
	var tr ptsTracker
	if got, ok := packetPTS(pesPacket(123456789)); !ok || got != 123456789 {
		t.Fatalf("packetPTS = %d %v", got, ok)
	}
	b := append(pesPacket(90000*10), pesPacket(90000*13)...)
	b = append(b, make([]byte, tsPacket)...) // non-PES packet ignored
	if !tr.scan(b) || tr.span() != 3 {
		t.Errorf("span = %v", tr.span())
	}
}

func TestRemoveUnlisted(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"seg-1.ts", "seg-2.ts", "seg-3.ts", "seg-9.ts"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte{0x47}, 0o644)
	}
	pl := "#EXTM3U\n#EXT-X-DISCONTINUITY\n#EXTINF:2.0,\nseg-3.ts\n#EXTINF:2.0,\nseg-9.ts\n"
	keep := removeUnlisted(dir, []string{"seg-1.ts", "seg-2.ts", "seg-3.ts"}, pl)
	if len(keep) != 1 || keep[0] != "seg-3.ts" {
		t.Errorf("keep = %v", keep)
	}
	for n, want := range map[string]bool{"seg-1.ts": false, "seg-2.ts": false, "seg-3.ts": true, "seg-9.ts": true} {
		_, err := os.Stat(filepath.Join(dir, n))
		if (err == nil) != want {
			t.Errorf("%s exists=%v want %v", n, err == nil, want)
		}
	}
	got := listSegments(dir)
	if len(got) != 2 {
		t.Errorf("listSegments = %v", got)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
