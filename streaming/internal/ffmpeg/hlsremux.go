package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Story 58.02: a TV session whose video plays as it is but whose container,
// audio or Dolby Vision layer does not is remuxed into HLS (or DASH): the video
// copied, never encoded, so a 4K HDR file costs no transcode slot and keeps
// its picture.

// SegmentsFMP4 and SegmentsTS are the HLS segment formats.
const (
	SegmentsTS   = "ts"
	SegmentsFMP4 = "fmp4"
)

// RemuxPlan is how a remux session's stream is made (Job.Remux).
type RemuxPlan struct {
	// Segments is the HLS segment format ("ts", "fmp4"); a DASH remux is
	// always fMP4.
	Segments string
	// VideoCodec is the source's video codec (the probe's): HEVC is tagged
	// hvc1 in fMP4, as Apple's and Cast's players ask.
	VideoCodec string
	// Audio: the source has an audio stream; AudioCodec is what it is encoded
	// to ("eac3": 5.1 at 640 kb/s, "aac": stereo at 192 kb/s; "": copied).
	Audio      bool
	AudioCodec string
	// SourceAudio is the source's audio codec (the master playlist's CODECS
	// when it is copied).
	SourceAudio string
	// DropDV: the Dolby Vision RPU and enhancement layer are dropped, the base
	// layer (HDR10, HLG, SDR) sent alone; StripDV is the bitstream filter that
	// does it (DVStripFilter, picked by the runner for its ffmpeg; "" none).
	DropDV  bool
	StripDV string
	// StereoAAC: a stereo AAC track beside the first (Roku), as a second audio
	// rendition.
	StereoAAC bool
	// The source's size, bitrate, bit depth and colour range ("PQ", "HLG",
	// "": SDR), for the master playlist's STREAM-INF.
	Width, Height, BitrateKbps int
	TenBit                     bool
	VideoRange                 string
}

// DVStripFilter is the bitstream filter that sends a Dolby Vision stream's base
// layer alone: filter_units dropping the RPU (NAL type 62) and enhancement
// layer (63) units, which every ffmpeg the home runs on has (4.4 on), then,
// where ffmpeg has it (7.1 on), dovi_rpu=strip=1, which drops the stream's
// Dolby Vision configuration record too (an fMP4 init then names no dvcC box).
// dovi_rpu alone is not enough: it keeps an RPU it cannot parse.
func DVStripFilter(haveDoviRPU bool) string {
	f := "filter_units=remove_types=62|63"
	if haveDoviRPU {
		f += ",dovi_rpu=strip=1"
	}
	return f
}

// stereo: the stream carries a second (stereo AAC) audio track.
func (p RemuxPlan) stereo() bool { return p.Audio && p.StereoAAC }

// remuxStreams are the video and audio options of a remux: the video copied
// (the Dolby Vision layer dropped when asked, HEVC tagged hvc1), the audio
// copied or encoded, and the stereo AAC track after it.
func remuxStreams(p RemuxPlan, fmp4 bool) Args {
	a := Args{"-map", "0:v:0", "-c:v", "copy"}
	if p.StripDV != "" {
		a = append(a, "-bsf:v", p.StripDV)
	}
	if fmp4 && isHEVC(p.VideoCodec) {
		a = append(a, "-tag:v", "hvc1")
	}
	if !p.Audio {
		return a
	}
	a = append(a, "-map", "0:a:0")
	switch p.AudioCodec {
	case "eac3":
		a = append(a, "-c:a:0", "eac3", "-b:a:0", "640k", "-ac:a:0", "6")
	case "aac":
		a = append(a, "-c:a:0", "aac", "-b:a:0", "192k", "-ac:a:0", "2")
	default:
		a = append(a, "-c:a:0", "copy")
	}
	if p.stereo() {
		a = append(a, "-map", "0:a:0", "-c:a:1", "aac", "-b:a:1", "192k", "-ac:a:1", "2")
	}
	return a
}

func isHEVC(codec string) bool {
	switch strings.ToLower(codec) {
	case "hevc", "h265", "hev1", "hvc1":
		return true
	}
	return false
}

// HLSRemuxArgs is a remux session's ffmpeg: the source copied into HLS (one
// video variant at v0/; with a stereo track, the audio as renditions v1/, v2/),
// in TS or fMP4 segments (init.mp4 and seg-N.m4s, the playlists' #EXT-X-MAP).
// Each playlist is an EVENT one, as a transcode's: the remux runs far ahead of
// playback, and the viewer seeks anywhere it has written.
func HLSRemuxArgs(input, outputDir string, p RemuxPlan) Args {
	fmp4 := p.Segments == SegmentsFMP4
	args := Args{"-y", "-hide_banner", "-loglevel", "warning", "-i", input}
	args = append(args, remuxStreams(p, fmp4)...)
	ext := "ts"
	args = append(args,
		"-f", "hls",
		"-hls_time", itoa(segmentSeconds),
		"-hls_playlist_type", "event",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments",
	)
	if fmp4 {
		ext = "m4s"
		args = append(args, "-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4")
	}
	if p.stereo() {
		// the video and each audio track their own playlist, the audio an
		// EXT-X-MEDIA group (BuildRemuxMaster names it the same)
		args = append(args, "-var_stream_map", "v:0,agroup:"+remuxAudioGroup+" a:0,agroup:"+remuxAudioGroup+" a:1,agroup:"+remuxAudioGroup)
	} else {
		streams := "v:0"
		if p.Audio {
			streams += ",a:0"
		}
		args = append(args, "-var_stream_map", streams)
	}
	return append(args,
		"-hls_segment_filename", outputDir+"/v%v/seg-%d."+ext,
		// no -master_pl_name: the orchestrator's master (BuildRemuxMaster, with
		// the copied codecs and the colour range) is the one served
		outputDir+"/v%v/index.m3u8",
	)
}

// DASHRemuxArgs is a remux session in DASH: the video copied (Roku plays AV1
// only by DASH), the audio its own AdaptationSet.
func DASHRemuxArgs(input, outputDir string, p RemuxPlan) Args {
	p.StereoAAC = false // DASH's own audio sets: one track
	args := Args{"-y", "-hide_banner", "-loglevel", "warning", "-i", input}
	args = append(args, remuxStreams(p, true)...)
	sets := "id=0,streams=v"
	if p.Audio {
		sets += " id=1,streams=a"
	}
	return append(args,
		"-f", "dash",
		"-adaptation_sets", sets,
		"-seg_duration", itoa(segmentSeconds),
		"-window_size", "0",
		"-init_seg_name", "init-$RepresentationID$.m4s",
		"-media_seg_name", "chunk-$RepresentationID$-$Number$.m4s",
		outputDir+"/manifest.mpd",
	)
}

// remuxAudioGroup is the audio rendition group of a remux with a stereo track.
const remuxAudioGroup = "aud"

// codecTag is the master playlist's CODECS entry for a copied stream: the
// highest level the codec's streams here may need (naming a higher level than
// the stream's is safe; hlsVideoCodec).
func codecTag(codec string, tenBit bool) string {
	switch strings.ToLower(codec) {
	case "hevc", "h265", "hev1", "hvc1":
		if tenBit {
			return "hvc1.2.4.L153.B0" // Main 10, level 5.1
		}
		return "hvc1.1.6.L153.B0" // Main, level 5.1
	case "av1", "av01":
		if tenBit {
			return "av01.0.13M.10"
		}
		return "av01.0.13M.08"
	case "vp9", "vp09":
		if tenBit {
			return "vp09.02.51.10"
		}
		return "vp09.00.51.08"
	case "h264", "avc", "avc1":
		return "avc1.640033" // High, level 5.1
	case "aac":
		return "mp4a.40.2"
	case "ac3":
		return "ac-3"
	case "eac3":
		return "ec-3"
	case "opus":
		return "Opus"
	case "flac":
		return "fLaC"
	case "mp3":
		return "mp4a.40.34"
	}
	return ""
}

// BuildRemuxMaster is a remux session's master playlist: one variant, the
// source's size and bitrate, the copied codecs (CODECS) and its colour range
// (VIDEO-RANGE, for an HDR10 or HLG picture); with a stereo track, the two
// audio renditions as an EXT-X-MEDIA group.
func BuildRemuxMaster(p RemuxPlan) []byte {
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	if p.Segments == SegmentsFMP4 {
		sb.WriteString("#EXT-X-VERSION:7\n")
	} else {
		sb.WriteString("#EXT-X-VERSION:6\n")
	}
	sb.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	codecs := []string{}
	if c := codecTag(p.VideoCodec, p.TenBit); c != "" {
		codecs = append(codecs, c)
	}
	audio := p.AudioCodec
	if audio == "" {
		audio = p.SourceAudio
	}
	if p.Audio {
		if c := codecTag(audio, false); c != "" {
			codecs = append(codecs, c)
		}
		if p.stereo() && audio != "aac" {
			codecs = append(codecs, codecTag("aac", false))
		}
	}
	if p.stereo() {
		fmt.Fprintf(&sb, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=%q,NAME=\"Surround\",DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"6\",URI=\"v1/index.m3u8\"\n", remuxAudioGroup)
		fmt.Fprintf(&sb, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=%q,NAME=\"Stereo\",DEFAULT=NO,AUTOSELECT=YES,CHANNELS=\"2\",URI=\"v2/index.m3u8\"\n", remuxAudioGroup)
	}
	kbps := p.BitrateKbps
	if kbps <= 0 {
		kbps = 20000
	}
	inf := fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d", kbps*1000)
	if p.Width > 0 && p.Height > 0 {
		inf += fmt.Sprintf(",RESOLUTION=%dx%d", p.Width, p.Height)
	}
	if len(codecs) > 0 {
		inf += ",CODECS=\"" + strings.Join(codecs, ",") + "\""
	}
	if p.VideoRange != "" {
		inf += ",VIDEO-RANGE=" + p.VideoRange
	}
	if p.stereo() {
		inf += ",AUDIO=\"" + remuxAudioGroup + "\""
	}
	sb.WriteString(inf + "\n")
	sb.WriteString("v0/index.m3u8\n")
	return []byte(sb.String())
}

// listBSFs is the bitstream filters `ffmpeg -bsfs` names.
func listBSFs(ctx context.Context, ffmpeg string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-bsfs").Output()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.TrimSpace(l); f != "" && !strings.Contains(f, " ") {
			have[f] = true
		}
	}
	return have, nil
}
