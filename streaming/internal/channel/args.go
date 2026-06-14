package channel

import (
	"fmt"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
)

// segmentSeconds is the live HLS segment duration; windowSize bounds the
// sliding playlist (delete_segments keeps disk + latency bounded).
const (
	segmentSeconds = 6
	windowSize     = 6
)

// LiveHLSArgs builds the FFmpeg invocation for a channel's sliding live
// HLS window, fed by the concat demuxer (D2/D4). Differences from the
// VOD HLSArgs: the input is `-f concat -safe 0 -i <list>`, every program
// is normalised through the same ladder (no stream-copy of heterogeneous
// sources, EC3), and the playlist is live — delete_segments+append_list,
// no ENDLIST — so the window slides as programs roll.
func LiveHLSArgs(concatPath, outputDir, masterName string, ladder []ffmpeg.Rendition, hwaccel string) ffmpeg.Args {
	args := ffmpeg.Args{
		"-y", "-hide_banner", "-loglevel", "warning",
		// The concat demuxer needs unsafe paths allowed (absolute library
		// paths) and re-timestamping so boundaries are seamless.
		"-f", "concat", "-safe", "0",
	}
	if hwaccel != "" {
		args = append(args, "-hwaccel", hwaccel)
	}
	args = append(args, "-i", concatPath)

	for i, rung := range ladder {
		args = append(args,
			"-map", "0:v:0",
			"-c:v:"+itoa(i), encoderFor(hwaccel),
			"-b:v:"+itoa(i), itoa(rung.BitrateKbps)+"k",
			"-s:v:"+itoa(i), fmt.Sprintf("%dx%d", rung.Width, rung.Height),
			"-preset:v:"+itoa(i), "veryfast",
		)
	}
	args = append(args,
		"-map", "0:a:0",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", itoa(segmentSeconds),
		"-hls_list_size", itoa(windowSize),
		// Live window: rotate segments, append to the playlist, never
		// write ENDLIST (the stream is continuous).
		"-hls_flags", "delete_segments+append_list+independent_segments+omit_endlist",
		"-hls_segment_filename", outputDir+"/v%v/seg-%d.ts",
		"-master_pl_name", masterName,
		outputDir+"/v%v/index.m3u8",
	)
	return args
}

// MPEGTSArgs builds the continuous MPEG-TS mux a single HDHomeRun tuner
// pulls (D9 / Story 27.5). It writes one program to stdout (pipe:1),
// joined at the wall-clock offset via the same concat input, maintaining
// PID/PCR continuity across concat boundaries.
func MPEGTSArgs(concatPath string, ladder []ffmpeg.Rendition, hwaccel string) ffmpeg.Args {
	args := ffmpeg.Args{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-f", "concat", "-safe", "0",
	}
	if hwaccel != "" {
		args = append(args, "-hwaccel", hwaccel)
	}
	args = append(args, "-i", concatPath)

	// HDHomeRun consumers take a single program (not an ABR ladder);
	// transcode to the top rung (or a single 720p default) + AAC.
	rung := topRung(ladder)
	args = append(args,
		"-map", "0:v:0",
		"-c:v", encoderFor(hwaccel),
		"-b:v", itoa(rung.BitrateKbps)+"k",
		"-s", fmt.Sprintf("%dx%d", rung.Width, rung.Height),
		"-preset", "veryfast",
		"-map", "0:a:0",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "mpegts",
		"-mpegts_flags", "+resend_headers",
		"pipe:1",
	)
	return args
}

func topRung(ladder []ffmpeg.Rendition) ffmpeg.Rendition {
	if len(ladder) > 0 {
		return ladder[0]
	}
	return ffmpeg.Rendition{Name: "v0", Height: 720, Width: 1280, BitrateKbps: 3000, Codec: "h264"}
}

// encoderFor maps a hwaccel name to its encoder, defaulting to software
// libx264. (The VOD path's encoder mapping lives in the ffmpeg package
// but is unexported; this mirrors the common cases the channel engine
// needs.)
func encoderFor(hwaccel string) string {
	switch hwaccel {
	case "videotoolbox":
		return "h264_videotoolbox"
	case "qsv":
		return "h264_qsv"
	case "nvenc", "cuda":
		return "h264_nvenc"
	case "vaapi":
		return "h264_vaapi"
	default:
		return "libx264"
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	if neg {
		d = append([]byte{'-'}, d...)
	}
	return string(d)
}
