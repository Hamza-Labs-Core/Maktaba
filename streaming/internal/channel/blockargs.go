package channel

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// OutputProfile is the fixed format every block of a channel is
// normalised to, so the persistent muxer can stream-copy block after
// block into one continuous output.
type OutputProfile struct {
	Width, Height int
	FPS           int
	VideoKbps     int
	AudioCodec    string // "aac" (default) | "ac3"
	AudioKbps     int
	// Encoder is "nvenc" (h264_nvenc) or "software" (libx264).
	Encoder string
	// SegmentSeconds is the HLS target segment duration.
	SegmentSeconds int
	// HLSListSize is the sliding window length (segments).
	HLSListSize int
}

// Encoder names.
const (
	EncoderNVENC    = "nvenc"
	EncoderSoftware = "software"
)

// DefaultProfile is 1080p30 H.264 6 Mb/s + AAC 192k stereo, 4 s segments.
func DefaultProfile() OutputProfile {
	return OutputProfile{Width: 1920, Height: 1080, FPS: 30, VideoKbps: 6000,
		AudioCodec: "aac", AudioKbps: 192, Encoder: EncoderSoftware,
		SegmentSeconds: 4, HLSListSize: 10}
}

func (p OutputProfile) normalised() OutputProfile {
	d := DefaultProfile()
	if p.Width <= 0 || p.Height <= 0 {
		p.Width, p.Height = d.Width, d.Height
	}
	// Even dimensions (yuv420p).
	p.Width -= p.Width % 2
	p.Height -= p.Height % 2
	if p.FPS <= 0 {
		p.FPS = d.FPS
	}
	if p.VideoKbps <= 0 {
		p.VideoKbps = d.VideoKbps
	}
	if p.AudioCodec != "ac3" {
		p.AudioCodec = "aac"
	}
	if p.AudioKbps <= 0 {
		p.AudioKbps = d.AudioKbps
	}
	if p.Encoder != EncoderNVENC {
		p.Encoder = EncoderSoftware
	}
	if p.SegmentSeconds <= 0 {
		p.SegmentSeconds = d.SegmentSeconds
	}
	if p.HLSListSize <= 0 {
		p.HLSListSize = d.HLSListSize
	}
	return p
}

// keyintSeconds is the forced keyframe interval; HLS segments cut on it.
const keyintSeconds = 2

// BlockInput describes one block's ffmpeg invocation.
type BlockInput struct {
	// Kind: "file" (library media), "live" (stream URL), "slate".
	Kind        string
	Source      string  // file path or stream URL (file/live)
	SeekSec     float64 // input seek (file only)
	DurationSec float64 // output length (always set)
	HasVideo    bool    // source has a video stream (file/live)
	HasAudio    bool    // source has an audio stream; false → anullsrc
	HWDecode    bool    // add -hwaccel cuda (NVENC hosts, file inputs)

	Watermark *Watermark // overlaid on file/live
	Loudnorm  *Loudnorm  // applied to file/live audio when enabled

	// Slate visuals: SlateImage fills the frame (the loading picture);
	// else SlateLogo is centred on black; else plain black. SlateText is
	// drawn when FontFile is set.
	SlateImage string
	SlateLogo  string
	SlateText  string
	FontFile   string

	// TSOffsetSec shifts the block's output timestamps onto the channel
	// timeline so consecutive blocks are monotonic in the muxer.
	TSOffsetSec float64
}

// BlockArgs builds the ffmpeg args for one block: read in real time
// (-re), normalise to the profile, write MPEG-TS to stdout.
func BlockArgs(p OutputProfile, in BlockInput) []string {
	p = p.normalised()
	W, H, fps := itoa(p.Width), itoa(p.Height), itoa(p.FPS)
	args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error"}

	var filters []string
	nextInput := 0
	addInput := func(a ...string) int {
		args = append(args, a...)
		nextInput++
		return nextInput - 1
	}
	blackSrc := "color=c=black:s=" + W + "x" + H + ":r=" + fps
	silenceSrc := "anullsrc=channel_layout=stereo:sample_rate=48000"
	fitPad := "scale=" + W + ":" + H + ":force_original_aspect_ratio=decrease,pad=" + W + ":" + H +
		":(ow-iw)/2:(oh-ih)/2:color=black,setsar=1"

	var vLabel, aMap string
	switch in.Kind {
	case "file", "live":
		var main []string
		if in.Kind == "file" {
			if in.HWDecode && in.HasVideo {
				main = append(main, "-hwaccel", "cuda")
			}
			if in.SeekSec > 0 {
				main = append(main, "-ss", formatSec(in.SeekSec))
			}
		} else if isHTTP(in.Source) {
			main = append(main, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5")
		}
		// Bound the INPUT to the block length too: filters with
		// look-ahead (loudnorm) then flush at EOF instead of making the
		// block overrun its slot by their latency.
		if in.DurationSec > 0 {
			main = append(main, "-t", formatSec(in.DurationSec))
		}
		main = append(main, "-re", "-i", in.Source)
		src := addInput(main...)

		vIn := itoa(src) + ":v:0"
		if !in.HasVideo {
			vIn = itoa(addInput("-re", "-f", "lavfi", "-i", blackSrc)) + ":v"
		}
		filters = append(filters, "["+vIn+"]"+fitPad+",fps="+fps+",format=yuv420p[vb]")
		vLabel = "vb"
		if wm := in.Watermark; wm != nil && wm.Path != "" {
			wi := addInput(imageInput(wm.Path)...)
			ww := int(float64(p.Width)*wm.WidthPct/100.0) &^ 1
			if ww < 2 {
				ww = 2
			}
			m := int(float64(p.Height) * wm.MarginPct / 100.0)
			x, y := overlayXY(wm.Position, m)
			filters = append(filters,
				"["+itoa(wi)+":v]scale="+itoa(ww)+":-2,format=rgba[wm]",
				"[vb][wm]overlay=x="+x+":y="+y+":shortest=1:format=auto,format=yuv420p[vo]")
			vLabel = "vo"
		}

		if in.HasAudio {
			chain := "[" + itoa(src) + ":a:0]aresample=async=1000"
			if ln := in.Loudnorm; ln != nil && ln.Enabled {
				chain += "," + loudnessFilter(*ln)
			}
			chain += ",aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo[ao]"
			filters = append(filters, chain)
			aMap = "[ao]"
		} else {
			aMap = itoa(addInput("-f", "lavfi", "-i", silenceSrc)) + ":a"
		}

	default: // slate
		switch {
		case in.SlateImage != "":
			bi := addInput(append([]string{"-re"}, imageInput(in.SlateImage)...)...)
			filters = append(filters, "["+itoa(bi)+":v]"+fitPad+",fps="+fps+",format=yuv420p[vb]")
		case in.SlateLogo != "":
			bi := addInput("-re", "-f", "lavfi", "-i", blackSrc)
			li := addInput(imageInput(in.SlateLogo)...)
			box := itoa((p.Width*2/5)&^1) + ":" + itoa((p.Height*2/5)&^1)
			filters = append(filters,
				"["+itoa(li)+":v]scale="+box+":force_original_aspect_ratio=decrease,format=rgba[logo]",
				"["+itoa(bi)+":v][logo]overlay=x=(W-w)/2:y=(H-h)/2:shortest=1,fps="+fps+",format=yuv420p[vb]")
		default:
			bi := addInput("-re", "-f", "lavfi", "-i", blackSrc)
			filters = append(filters, "["+itoa(bi)+":v]format=yuv420p[vb]")
		}
		vLabel = "vb"
		if in.SlateText != "" && in.FontFile != "" {
			filters[len(filters)-1] = strings.TrimSuffix(filters[len(filters)-1], "[vb]") +
				",drawtext=fontfile='" + escapeFilterPath(in.FontFile) + "':text='" + escapeDrawtext(in.SlateText) +
				"':fontcolor=white:fontsize=" + itoa(p.Height/20) +
				":x=(w-text_w)/2:y=h-text_h-" + itoa(p.Height/10) + "[vb]"
		}
		aMap = itoa(addInput("-f", "lavfi", "-i", silenceSrc)) + ":a"
	}

	args = append(args, "-filter_complex", strings.Join(filters, ";"),
		"-map", "["+vLabel+"]", "-map", aMap)
	args = append(args, videoEncoderArgs(p)...)
	args = append(args, audioEncoderArgs(p)...)
	if in.DurationSec > 0 {
		args = append(args, "-t", formatSec(in.DurationSec))
	}
	args = append(args,
		"-max_muxing_queue_size", "4096",
		"-output_ts_offset", formatSec(in.TSOffsetSec),
		// Flag each block's first packets as a discontinuity so the
		// muxer's demuxer doesn't treat the CC reset as corruption.
		"-mpegts_flags", "+initial_discontinuity",
		"-f", "mpegts", "pipe:1")
	return args
}

// videoEncoderArgs: H.264 CBR-ish with a keyframe every keyintSeconds.
func videoEncoderArgs(p OutputProfile) []string {
	gop := itoa(p.FPS * keyintSeconds)
	rate := itoa(p.VideoKbps) + "k"
	buf := itoa(p.VideoKbps*2) + "k"
	kf := "expr:gte(t,n_forced*" + itoa(keyintSeconds) + ")"
	if p.Encoder == EncoderNVENC {
		return []string{"-c:v", "h264_nvenc", "-preset", "p4", "-tune", "ll", "-rc", "cbr",
			"-b:v", rate, "-maxrate", rate, "-bufsize", buf, "-profile:v", "high",
			"-g", gop, "-bf", "0", "-forced-idr", "1", "-force_key_frames", kf}
	}
	return []string{"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high",
		"-b:v", rate, "-maxrate", rate, "-bufsize", buf,
		"-g", gop, "-keyint_min", gop, "-sc_threshold", "0", "-force_key_frames", kf}
}

func audioEncoderArgs(p OutputProfile) []string {
	codec := "aac"
	if p.AudioCodec == "ac3" {
		codec = "ac3"
	}
	return []string{"-c:a", codec, "-b:a", itoa(p.AudioKbps) + "k", "-ar", "48000", "-ac", "2"}
}

func loudnessFilter(l Loudnorm) string {
	if l.Filter == "dynaudnorm" {
		return "dynaudnorm=f=250:g=15"
	}
	t := l.TargetLUFS
	if t == 0 {
		t = DefaultTargetLUFS
	}
	return "loudnorm=I=" + strconv.FormatFloat(t, 'f', -1, 64) + ":TP=-1.5:LRA=11"
}

// imageInput returns the input args for a still or animated overlay. An
// animated GIF loops forever (-ignore_loop 0); stills loop as a frame.
func imageInput(path string) []string {
	if strings.EqualFold(filepath.Ext(path), ".gif") {
		return []string{"-ignore_loop", "0", "-i", path}
	}
	return []string{"-loop", "1", "-i", path}
}

func overlayXY(position string, margin int) (string, string) {
	m := itoa(margin)
	switch position {
	case "top-left":
		return m, m
	case "bottom-left":
		return m, "H-h-" + m
	case "bottom-right":
		return "W-w-" + m, "H-h-" + m
	default: // top-right
		return "W-w-" + m, m
	}
}

// MuxerArgs builds the persistent per-channel muxer: MPEG-TS blocks in on
// stdin, stream-copied into (1) a sliding live HLS window in outDir and
// (2) a continuous MPEG-TS on stdout for the fan-out hub.
func MuxerArgs(p OutputProfile, outDir string) []string {
	p = p.normalised()
	dir := filepath.ToSlash(outDir)
	return []string{
		"-hide_banner", "-nostats", "-loglevel", "warning",
		"-fflags", "+genpts",
		"-probesize", "1000000", "-analyzeduration", "1500000",
		"-f", "mpegts", "-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0", "-c", "copy",
		"-f", "hls",
		"-hls_time", itoa(p.SegmentSeconds),
		"-hls_list_size", itoa(p.HLSListSize),
		"-hls_delete_threshold", "3",
		"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time+temp_file",
		"-hls_start_number_source", "epoch",
		"-hls_segment_filename", dir + "/seg-%d.ts",
		dir + "/index.m3u8",
		"-map", "0:v:0", "-map", "0:a:0", "-c", "copy",
		"-f", "mpegts", "-mpegts_flags", "+resend_headers", "pipe:1",
	}
}

// MasterPlaylist is the single-rendition master written next to the
// live index so players that want a master get one.
func MasterPlaylist(p OutputProfile) string {
	p = p.normalised()
	audio := "mp4a.40.2"
	if p.AudioCodec == "ac3" {
		audio = "ac-3"
	}
	bw := (p.VideoKbps + p.AudioKbps) * 1000 * 11 / 10
	return fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-INDEPENDENT-SEGMENTS\n"+
		"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,FRAME-RATE=%d,CODECS=\"avc1.640028,%s\"\nindex.m3u8\n",
		bw, p.Width, p.Height, p.FPS, audio)
}

func isHTTP(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// escapeFilterPath escapes a path for use inside a single-quoted filter
// option (Windows drive colons and backslashes).
func escapeFilterPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.ReplaceAll(p, `'`, `'\''`)
	return strings.ReplaceAll(p, ":", `\:`)
}

func escapeDrawtext(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, "’", ":", `\:`, "%", `\%`)
	return r.Replace(s)
}

// formatSec renders seconds with millisecond precision, trimming zeros.
func formatSec(s float64) string {
	str := strconv.FormatFloat(s, 'f', 3, 64)
	if strings.Contains(str, ".") {
		str = strings.TrimRight(str, "0")
		str = strings.TrimRight(str, ".")
	}
	return str
}

func itoa(i int) string { return strconv.Itoa(i) }
