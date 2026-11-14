package broadcast

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
)

// The slate's words, in English and Arabic (the edge loops it while the channel is away).
const (
	slateOfflineEN = "Offline. Back soon."
	slateOfflineAR = "غير متصل. نعود قريبًا."
)

// slateFonts are fonts with Arabic letters, the first found used.
var slateFonts = []string{
	"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
	"/System/Library/Fonts/GeezaPro.ttc",
	"/usr/share/fonts/truetype/noto/NotoSansArabic-Regular.ttf",
	"/usr/share/fonts/opentype/noto/NotoSansArabic-Regular.ttf",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/dejavu/DejaVuSans.ttf",
}

func slateFont() string {
	for _, f := range slateFonts {
		if _, err := os.Stat(f); err == nil {
			return f
		}
	}
	return ""
}

// drawtextEscape escapes a drawtext text value.
func drawtextEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `:`, `\:`, `%`, `\%`, `,`, `\,`)
	return r.Replace(s)
}

// SlateArgs make one 4 s segment of the offline slate (720p, silent audio): the channel's
// name over the offline words in English and Arabic, or a plain card without a font.
func SlateArgs(name, font, out string) []string {
	vf := "format=yuv420p"
	if font != "" {
		ff := "fontfile='" + strings.ReplaceAll(font, "'", `\'`) + "'"
		line := func(text string, size, y int, shaping bool) string {
			s := "drawtext=" + ff + ":text='" + drawtextEscape(text) + "':fontcolor=white:fontsize=" +
				itoa(size) + ":x=(w-text_w)/2:y=" + itoa(y)
			if shaping {
				s += ":text_shaping=1"
			}
			return s
		}
		vf = strings.Join([]string{line(name, 56, 260, true), line(slateOfflineEN, 36, 360, false),
			line(slateOfflineAR, 36, 420, true), vf}, ",")
	}
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "color=c=0x101418:s=1280x720:r=25",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", itoa(SegmentSeconds), "-vf", vf,
		"-c:v", "libx264", "-preset", "veryfast", "-g", "50", "-b:v", "600k",
		"-c:a", "aac", "-b:a", "64k", "-f", "mpegts", out}
}

func itoa(n int) string { return strconv.Itoa(n) }

// uploadSlate makes the channel's slate segment and PUTs it as slate/0.ts, once per token.
func (c *Ctl) uploadSlate(ctx context.Context, j Job) {
	dir := filepath.Join(c.Dir, "slate-"+j.Channel.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := filepath.Join(dir, "0.ts")
	runner := c.Runner
	if runner == nil {
		runner = relay.ExecRunner{}
	}
	bin := c.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	run := func(font string) bool {
		p, err := runner.Start(bin, SlateArgs(j.Name, font, out))
		if err != nil {
			return false
		}
		_ = p.Stdin().Close()
		done := make(chan error, 1)
		go func() { done <- p.Wait() }()
		select {
		case err := <-done:
			return err == nil
		case <-time.After(30 * time.Second):
			p.Kill()
			<-done
			return false
		}
	}
	// a build without drawtext (or text shaping) still makes the plain card
	if !run(slateFont()) && !run("") {
		c.log().Warn("broadcast: the offline slate could not be made", "channel", j.Channel, "event", "broadcast_slate_error")
		return
	}
	up := &Uploader{HTTP: c.HTTP, Now: c.Now, Creds: func() (Creds, bool) { return c.creds(j.Channel) }}
	if up.Put(ctx, Segment{Rung: "slate", Seq: 0, Path: out, Duration: SegmentSeconds}) != Sent {
		c.mu.Lock()
		delete(c.slateFor, j.Channel) // tried again at the next sync
		c.mu.Unlock()
	}
}
