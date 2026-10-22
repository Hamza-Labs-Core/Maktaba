package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// What Maktaba asks of ffmpeg: the command lines of streaming's TV blocks,
// relays and transcodes, the pipeline's stages and the api's previews. A Linux
// machine's own ffmpeg must have all of it; the Mac's and Windows' payloads do.
var (
	// MinVersion: speechnorm, the TV's default loudness filter, is FFmpeg 4.4's
	// (Ubuntu 22.04 has 4.4.2, Debian 12 5.1). Nothing else asks for more: the
	// HLS segment options' 5.0 name is told apart where it is used.
	MinVersion = [2]int{4, 4}
	// Encoders: H.264 in software (the TV falls back to it whenever no GPU
	// encoder is free, and slates always use it), AAC, and what the pipeline's
	// thumbnails, frame hashes and audio extraction write.
	Encoders = []string{"libx264", "aac", "mjpeg", "rawvideo", "pcm_s16le"}
	// Decoders beyond the library's own codecs: the TV's images (watermark,
	// logo, loading image: PNG, JPEG, GIF) and yt-dlp's WebP thumbnails.
	Decoders = []string{"h264", "aac", "png", "mjpeg", "gif", "webp"}
	// Filters the TV blocks (lavfi colour and silence, scaling, padding, the
	// watermark, the slate's text, the loudness filters), the thumbnails and
	// the production-style stage (scene and signal statistics) are made of.
	Filters = []string{
		"color", "anullsrc", "setpts", "scale", "pad", "setsar", "fps", "format", "overlay", "drawtext",
		"split", "crop", "tile", "aresample", "aformat", "speechnorm", "loudnorm", "dynaudnorm",
		"scdet", "signalstats", "metadata",
	}
	// Muxers: the TV's MPEG-TS and HLS, the probes' null, the pipeline's
	// frames and audio.
	Muxers = []string{"mpegts", "hls", "null", "rawvideo", "image2", "image2pipe", "s16le"}
	// Demuxers beyond the library's containers: lavfi (libavdevice), the TV's
	// relayed MPEG-TS, its images.
	Demuxers = []string{"mpegts", "lavfi", "image2", "gif", "mov", "matroska"}
	// Protocols: files and pipes (the TV's blocks and relays).
	Protocols = []string{"file", "pipe"}
	// HWEncoders are the GPU H.264 encoders the TV and the transcodes use when
	// the machine has the device; none is required.
	HWEncoders = []string{"h264_nvenc", "h264_qsv", "h264_vaapi", "h264_videotoolbox", "h264_amf", "h264_mf"}
	// testEncode is a TV block in small: H.264 in software and AAC through the
	// default loudness filter, as the blocks set it (blockargs.go).
	testEncode = []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=320x180:r=25:d=0.4",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-af", "aresample=async=1000,speechnorm=e=6.25:r=0.00001:l=1,aresample=48000",
		"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", "-f", "mpegts", "-"}
)

// Report is what Check found: the tools, ffmpeg's version, and whether the
// roles can run them.
type Report struct {
	Tools
	// Version is ffmpeg's as it prints it ("4.4.2-0ubuntu0.22.04.1", "n7.1").
	Version string `json:"version,omitempty"`
	OK      bool   `json:"ok"`
	// Problem is why the roles cannot use these tools; Install, on Linux, how
	// to get the ffmpeg they need on this distribution. Nil when OK.
	Problem *Note `json:"problem,omitempty"`
	Install *Note `json:"install,omitempty"`
	// Warnings are what a feature beside the library needs and this ffmpeg
	// lacks (the audio fingerprints' chromaprint, HTTPS live sources).
	Warnings []Note `json:"warnings,omitempty"`
	// HWEncoders are the GPU H.264 encoders this ffmpeg has.
	HWEncoders []string  `json:"hw_encoders,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Note is a finding as the screens show it: a code (setup.check.note.<code>
// in the app's words), its values, and its English.
type Note struct {
	Code    string         `json:"code"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`
}

// run runs a tool and returns what it printed (tests replace it).
var run = func(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			err = fmt.Errorf("%w: %s", err, lastLine(string(out)))
		}
	}
	return string(out), err
}

// osRelease is /etc/os-release (tests replace it).
var osRelease = readOSRelease

// Check finds the tools (Find) and runs them: ffmpeg's version, encoders,
// filters and formats against what Maktaba asks, and ffprobe. On Linux a
// problem comes with the command that installs the distribution's ffmpeg.
func Check(ctx context.Context, t Tools) Report {
	r := Report{Tools: t, CheckedAt: time.Now().UTC()}
	r.Problem = r.check(ctx)
	r.OK = r.Problem == nil
	if !r.OK {
		r.Install = installNote(osRelease(), r.Problem.Code)
	}
	return r
}

func (r *Report) check(ctx context.Context) *Note {
	t := r.Tools
	switch {
	case t.FFmpeg == "" && (t.Source == FromConfig || t.Source == FromEnv): // its setting names nothing there
		return notFound(t)
	case t.FFmpeg == "":
		return note("ffmpeg_missing", "Maktaba needs ffmpeg to read and play the library, and this machine has none on the PATH.")
	}
	out, err := run(ctx, t.FFmpeg, "-hide_banner", "-version")
	if err != nil {
		return note("ffmpeg_broken", fmt.Sprintf("%s does not run: %v", t.FFmpeg, err), "path", t.FFmpeg, "error", err.Error())
	}
	r.Version = parseVersion(out)
	need := fmt.Sprintf("%d.%d", MinVersion[0], MinVersion[1])
	if v, ok := numericVersion(r.Version); ok && (v[0] < MinVersion[0] || v[0] == MinVersion[0] && v[1] < MinVersion[1]) {
		return note("ffmpeg_old", fmt.Sprintf("%s is FFmpeg %s: Maktaba needs %s or later.", t.FFmpeg, r.Version, need),
			"path", t.FFmpeg, "have", r.Version, "need", need)
	}
	have := map[string]map[string]bool{}
	for _, l := range []struct {
		arg   string
		parse func(string) map[string]bool
	}{
		{"-encoders", parseCodecs}, {"-decoders", parseCodecs}, {"-filters", parseFilters},
		{"-formats", parseFormats}, {"-protocols", parseProtocols},
	} {
		out, err := run(ctx, t.FFmpeg, "-hide_banner", l.arg)
		if err != nil {
			return note("ffmpeg_broken", fmt.Sprintf("%s %s fails: %v", t.FFmpeg, l.arg, err), "path", t.FFmpeg, "error", err.Error())
		}
		have[l.arg] = l.parse(out)
	}
	var missing []string
	for _, w := range []struct {
		arg, kind, prefix string
		names             []string
	}{
		{"-encoders", "encoder", "", Encoders}, {"-decoders", "decoder", "", Decoders},
		{"-filters", "filter", "", Filters}, {"-formats", "muxer", "E:", Muxers},
		{"-formats", "demuxer", "D:", Demuxers}, {"-protocols", "protocol", "in:", Protocols},
	} {
		for _, n := range w.names {
			if !have[w.arg][w.prefix+n] {
				missing = append(missing, w.kind+" "+n)
			}
		}
	}
	for _, hw := range HWEncoders {
		if have["-encoders"][hw] {
			r.HWEncoders = append(r.HWEncoders, hw)
		}
	}
	if !have["-formats"]["E:chromaprint"] {
		r.Warnings = append(r.Warnings, *note("ffmpeg_no_chromaprint",
			fmt.Sprintf("%s has no chromaprint: the audio fingerprints, which find the same recording twice, are not made.", t.FFmpeg),
			"path", t.FFmpeg))
	}
	if !have["-protocols"]["in:https"] {
		r.Warnings = append(r.Warnings, *note("ffmpeg_no_https",
			fmt.Sprintf("%s cannot open https:// addresses: a channel's live sources on the web do not play.", t.FFmpeg),
			"path", t.FFmpeg))
	}
	if len(missing) > 0 {
		list := strings.Join(missing, ", ")
		return note("ffmpeg_lacks", fmt.Sprintf("%s (FFmpeg %s) lacks what Maktaba uses: %s.", t.FFmpeg, r.Version, list),
			"path", t.FFmpeg, "have", r.Version, "missing", list)
	}
	if t.FFprobe == "" {
		if t.Setting != "" { // ffmpeg is here: the setting is ffprobe's
			return notFound(t)
		}
		return note("ffprobe_missing", fmt.Sprintf("ffmpeg is here (%s), but not ffprobe, which Maktaba reads the media with.", t.FFmpeg),
			"path", t.FFmpeg)
	}
	if _, err := run(ctx, t.FFprobe, "-hide_banner", "-version"); err != nil {
		return note("ffmpeg_broken", fmt.Sprintf("%s does not run: %v", t.FFprobe, err), "path", t.FFprobe, "error", err.Error())
	}
	// what it lists, it does: a TV block in small
	if _, err := run(ctx, t.FFmpeg, testEncode...); err != nil {
		return note("ffmpeg_broken", fmt.Sprintf("%s cannot encode H.264 and AAC: %v", t.FFmpeg, err), "path", t.FFmpeg, "error", err.Error())
	}
	return nil
}

func notFound(t Tools) *Note {
	return note("ffmpeg_not_found", fmt.Sprintf("%s names %s, which is not a program this user can run.", t.Setting, t.Wanted),
		"setting", t.Setting, "path", t.Wanted)
}

func note(code, message string, kv ...any) *Note {
	n := &Note{Code: code, Message: message}
	for i := 0; i+1 < len(kv); i += 2 {
		if n.Params == nil {
			n.Params = map[string]any{}
		}
		n.Params[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return n
}

// parseVersion is the version on ffmpeg's first line ("ffmpeg version 4.4.2-0ubuntu0.22.04.1
// Copyright ...").
func parseVersion(out string) string {
	line, _, _ := strings.Cut(out, "\n")
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "version" {
			return f[i+1]
		}
	}
	return ""
}

var versionNumbers = regexp.MustCompile(`^n?(\d+)\.(\d+)`)

// numericVersion is a release's major and minor ("4.4.2-0ubuntu0.22.04.1", "n7.1",
// "7.1.1-full_build"); a build from git ("N-115000-g1a2b3c4d") has none: its
// encoders and filters say what it can do.
func numericVersion(v string) ([2]int, bool) {
	m := versionNumbers.FindStringSubmatch(v)
	if m == nil {
		return [2]int{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return [2]int{major, minor}, true
}

// parseCodecs: `ffmpeg -encoders` or `-decoders`, a legend, " ------", then
// " V....D libx264  libx264 H.264 ...".
func parseCodecs(out string) map[string]bool {
	have := map[string]bool{}
	started := false
	for _, line := range lines(out) {
		f := strings.Fields(line)
		if !started {
			started = len(f) == 1 && strings.Trim(f[0], "-") == ""
			continue
		}
		if len(f) >= 2 {
			have[f[1]] = true
		}
	}
	return have
}

// parseFilters: `ffmpeg -filters`, " TS. loudnorm   A->A   EBU R128 ..." (the flags are two or
// three columns, by version).
func parseFilters(out string) map[string]bool {
	have := map[string]bool{}
	for _, line := range lines(out) {
		if f := strings.Fields(line); len(f) >= 3 && strings.Contains(f[2], "->") {
			have[f[1]] = true
		}
	}
	return have
}

// formatsLegend is the first line of `ffmpeg -formats`' legend: " D. = Demuxing supported" (4.x),
// " D.. = Demuxing supported" (since the device column).
var formatsLegend = regexp.MustCompile(`^ (D[.]+) = `)

// parseFormats: `ffmpeg -formats`, each name as "D:name" (a demuxer) and "E:name" (a muxer).
// The flags are a column as wide as the legend's and may hold spaces (" D d lavfi"), so
// they are cut by that width; a name may be several ("mov,mp4,m4a,3gp,3g2,mj2").
func parseFormats(out string) map[string]bool {
	have := map[string]bool{}
	width, started := 0, false
	for _, line := range lines(out) {
		if m := formatsLegend.FindStringSubmatch(line); m != nil && width == 0 {
			width = len(m[1])
			continue
		}
		if !started {
			started = width > 0 && strings.Trim(strings.TrimSpace(line), "-") == "" && strings.TrimSpace(line) != ""
			continue
		}
		if len(line) < 2+width {
			continue
		}
		flags, rest := line[1:1+width], strings.Fields(line[1+width:])
		if len(rest) == 0 {
			continue
		}
		for _, n := range strings.Split(rest[0], ",") {
			for _, f := range "DE" {
				if strings.ContainsRune(flags, f) {
					have[string(f)+":"+n] = true
				}
			}
		}
	}
	return have
}

// parseProtocols: `ffmpeg -protocols`, "Input:" then a name a line, "Output:" then the
// same; each as "in:name" or "out:name".
func parseProtocols(out string) map[string]bool {
	have := map[string]bool{}
	section := ""
	for _, line := range lines(out) {
		switch s := strings.TrimSpace(line); {
		case s == "Input:":
			section = "in:"
		case s == "Output:":
			section = "out:"
		case section != "" && s != "" && !strings.ContainsAny(s, " :"):
			have[section+s] = true
		}
	}
	return have
}

func lines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
	}
	return out
}

func lastLine(s string) string {
	ls := slices.DeleteFunc(lines(s), func(l string) bool { return strings.TrimSpace(l) == "" })
	if len(ls) == 0 {
		return ""
	}
	return strings.TrimSpace(ls[len(ls)-1])
}
