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

// filterPath quotes a path we chose (the font, the text files) for a filtergraph option.
func filterPath(p string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `'\''`, `:`, `\:`).Replace(p) + "'"
}

// SlateText writes the slate's three lines to files in dir (drawtext reads them with
// textfile=, so a channel's name never becomes part of the filtergraph).
func SlateText(dir, name string) ([3]string, error) {
	lines := [3]string{name, slateOfflineEN, slateOfflineAR}
	var paths [3]string
	for i, l := range lines {
		paths[i] = filepath.Join(dir, "line"+itoa(i)+".txt")
		// a name is one line; control characters are dropped
		clean := strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, l)
		if err := os.WriteFile(paths[i], []byte(clean), 0o600); err != nil {
			return paths, err
		}
	}
	return paths, nil
}

// SlateArgs make one 4 s segment of the offline slate (720p, silent audio): the channel's
// name over the offline words in English and Arabic (texts: SlateText's files), or a plain
// card without a font. Without text shaping (an ffmpeg built without HarfBuzz) Arabic
// would show as loose letters, so only the lines that need none are drawn: the English
// words, and the name when it has no right-to-left letters (rtlName).
func SlateArgs(texts [3]string, font string, shaping, rtlName bool, out string) []string {
	vf := "format=yuv420p"
	if font != "" && texts[0] != "" {
		line := func(file string, size, y int) string {
			s := "drawtext=fontfile=" + filterPath(font) + ":textfile=" + filterPath(file) +
				":expansion=none:fontcolor=white:fontsize=" + itoa(size) + ":x=(w-text_w)/2:y=" + itoa(y)
			if shaping {
				s += ":text_shaping=1"
			}
			return s
		}
		var parts []string
		if shaping || !rtlName {
			parts = append(parts, line(texts[0], 56, 260))
		}
		parts = append(parts, line(texts[1], 36, 360))
		if shaping {
			parts = append(parts, line(texts[2], 36, 420))
		}
		vf = strings.Join(append(parts, vf), ",")
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
	texts, err := SlateText(dir, j.Name)
	if err != nil {
		return
	}
	rtl := strings.IndexFunc(j.Name, func(r rune) bool { return r >= 0x0590 && r <= 0x08ff || r >= 0xfb1d && r <= 0xfefc }) >= 0
	run := func(font string, shaping bool) bool {
		p, err := runner.Start(bin, SlateArgs(texts, font, shaping, rtl, out))
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
	font := slateFont()
	if !run(font, true) && !run(font, false) && !run("", false) {
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
