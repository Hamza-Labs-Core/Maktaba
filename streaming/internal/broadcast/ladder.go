// Package broadcast is the home's broadcaster (Epic 50 story 50.22 §2): one per published
// channel, in the home's front door. It reads the channel's live TS (the push relay's
// source, which also keeps the channel placed and in demand while it runs), makes the HLS
// ladder with one ffmpeg (the top rung copied, the lower rungs encoded on the channel's
// keyframe grid), pushes each finished segment once to the broadcast edge with its hash and
// duration, posts the rungs' state every 10 s, keeps the control socket to the edge, and
// uploads the offline slate. What a channel airs stays the home's: the edge only carries it.
package broadcast

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
)

// Rung is one rendition. The API has the same table (api/internal/broadcast/ladder.go).
type Rung struct {
	Name                 string
	Height               int
	VideoKbps, AudioKbps int
}

// RungSource is the top rung: the channel's own encode, copied.
const RungSource = "src"

var lowerRungs = []Rung{
	{Name: "720p", Height: 720, VideoKbps: 3000, AudioKbps: 128},
	{Name: "480p", Height: 480, VideoKbps: 1400, AudioKbps: 96},
	{Name: "360p", Height: 360, VideoKbps: 800, AudioKbps: 96},
}

// LowerRung is an encoded rung of the table.
func LowerRung(name string) (Rung, bool) {
	for _, r := range lowerRungs {
		if r.Name == name {
			return r, true
		}
	}
	return Rung{}, false
}

// Encoder families the lower rungs use, chosen as the channel's own encoder is.
const (
	EncVideoToolbox = "videotoolbox"
	EncNVENC        = "nvenc"
	EncQSV          = "qsv"
	EncVAAPI        = "vaapi"
	EncSoftware     = "software"
)

// SegmentSeconds is the ladder's target duration (README §14.2).
const SegmentSeconds = 4

// LadderSpec is what one broadcaster's ffmpeg makes.
type LadderSpec struct {
	Rungs []string
	// Encoder is the family of the channel's own encoder.
	Encoder string
	// AllowSoftware is broadcast.allow_software_rungs (default off: about a core per rung).
	AllowSoftware bool
	// KeyintSeconds is the channel's keyframe grid (tv_output.keyint_seconds).
	KeyintSeconds int
	FPS           int
	// VAAPIDevice is the render node for VA-API (default /dev/dri/renderD128).
	VAAPIDevice string
	// Dir holds a directory per rung.
	Dir string
	// StartNumber is the first segment's number (the start time in 4 s slots, so a
	// restarted broadcaster never reuses a number the edge has seen).
	StartNumber int64
}

// ErrSoftwareRungs is a ladder with encoded rungs on a machine whose channel encoder is
// software, while broadcast.allow_software_rungs is off.
var ErrSoftwareRungs = errors.New("broadcast: the lower rungs need a hardware encoder (broadcast.allow_software_rungs is off)")

func videoArgs(enc string, r Rung, gop int, keyint int, vaapi string) []string {
	rate := strconv.Itoa(r.VideoKbps) + "k"
	buf := strconv.Itoa(2*r.VideoKbps) + "k"
	scale := "scale=-2:" + strconv.Itoa(r.Height)
	grid := []string{"-g", strconv.Itoa(gop), "-force_key_frames", "expr:gte(t,n_forced*" + strconv.Itoa(keyint) + ")"}
	rc := []string{"-b:v", rate, "-maxrate", rate, "-bufsize", buf}
	var a []string
	switch enc {
	case EncVideoToolbox:
		a = []string{"-vf", scale, "-c:v", "h264_videotoolbox", "-realtime", "1", "-profile:v", "high"}
	case EncNVENC:
		a = []string{"-vf", scale, "-c:v", "h264_nvenc", "-preset", "p4", "-tune", "ll", "-profile:v", "high"}
	case EncQSV:
		a = []string{"-vf", scale, "-c:v", "h264_qsv", "-preset", "veryfast", "-profile:v", "high"}
	case EncVAAPI:
		if vaapi == "" {
			vaapi = "/dev/dri/renderD128"
		}
		a = []string{"-vaapi_device", vaapi, "-vf", scale + ",format=nv12,hwupload", "-c:v", "h264_vaapi", "-profile:v", "high"}
	default:
		a = []string{"-vf", scale, "-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency", "-profile:v", "high"}
	}
	a = append(a, rc...)
	return append(a, grid...)
}

func hlsArgs(dir, rung string, start int64) []string {
	d := filepath.Join(dir, rung)
	return []string{"-f", "hls", "-hls_time", strconv.Itoa(SegmentSeconds), "-hls_list_size", "6",
		"-hls_flags", "independent_segments+program_date_time+omit_endlist",
		"-start_number", strconv.FormatInt(start, 10),
		"-hls_segment_filename", filepath.Join(d, "%d.ts"), filepath.Join(d, "index.m3u8")}
}

// Args are one ffmpeg's arguments for the whole ladder, reading the channel's TS on stdin:
// the top rung copied (its own watermark and strip already burned in), each lower rung
// encoded. The uploader deletes each segment once sent, so ffmpeg deletes none itself.
func Args(s LadderSpec) ([]string, error) {
	if len(s.Rungs) == 0 {
		return nil, errors.New("broadcast: a ladder needs a rung")
	}
	keyint := s.KeyintSeconds
	if keyint <= 0 || SegmentSeconds%keyint != 0 {
		keyint = 2
	}
	fps := s.FPS
	if fps <= 0 {
		fps = 30
	}
	enc := s.Encoder
	if enc != EncVideoToolbox && enc != EncNVENC && enc != EncQSV && enc != EncVAAPI {
		enc = EncSoftware
	}
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostdin", "-fflags", "+genpts", "-f", "mpegts", "-i", "pipe:0"}
	for i, name := range s.Rungs {
		args = append(args, "-map", "0:v:0", "-map", "0:a:0?")
		if name == RungSource {
			if i != 0 {
				return nil, fmt.Errorf("broadcast: %s must be the top rung", RungSource)
			}
			args = append(args, "-c", "copy")
		} else {
			r, ok := LowerRung(name)
			if !ok {
				return nil, fmt.Errorf("broadcast: unknown rung %q", name)
			}
			if enc == EncSoftware && !s.AllowSoftware {
				return nil, ErrSoftwareRungs
			}
			args = append(args, videoArgs(enc, r, fps*keyint, keyint, s.VAAPIDevice)...)
			args = append(args, "-c:a", "aac", "-b:a", strconv.Itoa(r.AudioKbps)+"k", "-ac", "2")
		}
		args = append(args, hlsArgs(s.Dir, name, s.StartNumber)...)
	}
	return args, nil
}

// Sessions counts the hardware encoder sessions a ladder holds (the copy rung holds none).
func Sessions(rungs []string) int {
	n := 0
	for _, r := range rungs {
		if r != RungSource {
			n++
		}
	}
	return n
}
