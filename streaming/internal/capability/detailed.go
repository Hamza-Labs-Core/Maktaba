package capability

import (
	"slices"
	"strconv"
	"strings"
)

// DecideFor is Decide for a session that may carry a device's report: the
// report merged into the profile (Apply), and what it asked beyond the
// platform's ceiling named in the verdict (Clamped, and in Reason).
func (r *Registry) DecideFor(profile *Profile, caps *DeviceCaps, src Source, ov Override) Verdict {
	eff, clamped := Apply(profile, caps)
	v := r.Decide(eff, src, ov)
	if len(clamped) > 0 {
		v.Clamped = clamped
		v.Reason += "; the device's report was clamped to the " + profile.Name + " ceiling (" + strings.Join(clamped, ", ") + ")"
	}
	return v
}

// has reports whether list names v, both read through canon.
func has(list []string, v string, canon func(string) string) bool {
	if v = canon(v); v == "" {
		return false
	}
	for _, l := range list {
		if canon(l) == v {
			return true
		}
	}
	return false
}

// sourceHDR is the source's HDR kind: its HDR field when the probe set one,
// else what its Dolby Vision record says.
func sourceHDR(src Source) string {
	if h := canonHDR(src.HDR); h != "" {
		return h
	}
	return "sdr"
}

// dvBase is the base layer a Dolby Vision source carries for a device without
// Dolby Vision ("" when it has none it can show: profile 5's IPT-PQ-c2 base is
// not a picture any other format reads).
//   - 8.1 → HDR10, 8.4 → HLG, 8.2 → SDR (8.6 is HDR10 too);
//   - 7 → its HDR10 base layer (the enhancement layer dropped);
//   - 5 → none.
func dvBase(src Source) string {
	switch src.DVProfile {
	case 7:
		return "hdr10"
	case 8:
		switch src.DVCompat {
		case 1, 6:
			return "hdr10"
		case 4:
			return "hlg"
		case 2:
			return "sdr"
		}
		return "hdr10" // a profile 8 file naming no compatibility is HDR10's in practice
	}
	return ""
}

// showsHDR reports whether the profile shows an HDR kind (SDR everywhere).
func showsHDR(p *Profile, kind string) bool {
	return kind == "sdr" || has(expandHDR(p.HDR), kind, canonHDR)
}

// segmentsFor is the HLS segment format a stream of video codec vcodec goes
// in on the profile ("" when none it plays carries it): HEVC, AV1 and VP9 go
// in fMP4 (Cast takes no HEVC in TS, and no player takes AV1 in TS); H.264
// stays in TS unless the device plays fMP4 alone (story 58.02 §3: no existing
// client sees a change).
func segmentsFor(p *Profile, vcodec string) string {
	segs := p.HLSSegments
	if len(segs) == 0 {
		segs = []string{"ts"}
	}
	fmp4 := slices.Contains(segs, "fmp4")
	if canonVideo(vcodec) != "h264" {
		if fmp4 {
			return "fmp4"
		}
		return ""
	}
	if slices.Contains(segs, "ts") {
		return "ts"
	}
	if fmp4 {
		return "fmp4"
	}
	return ""
}

// dolby reports whether an audio codec is one of Dolby's (a stereo AAC track
// goes beside it on Roku).
func dolby(codec string) bool {
	switch canonAudio(codec) {
	case "ac3", "eac3", "truehd", "ac4":
		return true
	}
	return false
}

// decideDetailed is a Detailed (TV) profile's verdict. In order:
//
//  1. force_transcode → transcode;
//  2. a picture taller, a bitrate higher, a codec, bit depth or frame rate the
//     device does not decode → transcode (H.264, tone-mapped to SDR for HDR);
//  3. Dolby Vision the device does not show: profile 8 and 7 go as their base
//     layer by remux (the RPU dropped) where the device shows that base;
//     profile 5, or a base it does not show → transcode; HDR10 or HLG it does
//     not show → transcode;
//  4. audio the device neither decodes nor passes through: TrueHD and DTS
//     become EAC3 where it plays EAC3, the rest AAC, beside the video copied
//     (remux);
//  5. a video codec the platform plays only by DASH → remux to DASH;
//  6. a container it does not play, or any of 3–4 → remux (HLS, fMP4 for
//     HEVC/AV1); none of them → direct.
func decideDetailed(p *Profile, src Source, ov Override) Verdict {
	vcodec := canonVideo(src.VideoCodec)
	transcode := func(reason string) Verdict {
		h := effectiveHeight(p, ov)
		if hh := p.heightFor("h264"); hh > 0 && (h == 0 || hh < h) {
			h = hh
		}
		return Verdict{
			Mode: ModeTranscode, Reason: reason,
			BitrateCapKbps: effectiveBitrate(p, ov),
			HeightCap:      h,
			Segments:       segmentsFor(p, "h264"),
		}
	}
	if ov.ForceTranscode {
		return transcode("force_transcode override")
	}
	if maxH := p.heightFor(vcodec); maxH > 0 && src.Height > maxH {
		return transcode("source height exceeds profile")
	}
	if p.MaxBitrateKbps > 0 && src.BitrateKbps > p.MaxBitrateKbps {
		return transcode("source bitrate exceeds profile")
	}
	if ov.MaxBitrateKbps > 0 && src.BitrateKbps > ov.MaxBitrateKbps {
		v := transcode("source bitrate exceeds override")
		v.BitrateCapKbps = ov.MaxBitrateKbps
		return v
	}
	if !has(p.VideoCodecs, vcodec, canonVideo) {
		return transcode("video codec not in profile")
	}
	if d := p.VideoBitDepths[vcodec]; d > 0 && src.BitDepth > d {
		return transcode("source bit depth exceeds the device's " + strconv.Itoa(d) + "-bit " + vcodec)
	}
	if f := p.VideoFPS[vcodec]; f > 0 && src.FPS > float64(f)+0.5 {
		return transcode("source frame rate exceeds the device's")
	}

	// 3. HDR and Dolby Vision
	stripDV := false
	why := []string{}
	if src.DVProfile > 0 {
		if !has(expandHDR(p.HDR), "dv-p"+strconv.Itoa(src.DVProfile), canonHDR) {
			base := dvBase(src)
			if base == "" || !showsHDR(p, base) {
				return transcode("Dolby Vision profile " + strconv.Itoa(src.DVProfile) +
					" without a base layer the device shows: tone-mapped transcode")
			}
			stripDV = true
			why = append(why, "Dolby Vision profile "+strconv.Itoa(src.DVProfile)+" as its "+base+" base layer")
		}
	} else if hdr := sourceHDR(src); !showsHDR(p, hdr) {
		return transcode(hdr + " not shown by the device: tone-mapped transcode")
	}

	// 4. audio
	audioOut := ""
	acodec := canonAudio(src.AudioCodec)
	hasAudio := src.AudioCodec != "" || src.AudioChannels > 0
	if hasAudio {
		plays := acodec != "" && (has(p.AudioCodecs, acodec, canonAudio) || has(p.Passthrough, acodec, canonAudio))
		if plays && p.MaxAudioChannels > 0 && src.AudioChannels > p.MaxAudioChannels &&
			!has(p.Passthrough, acodec, canonAudio) {
			plays = false
		}
		if !plays {
			audioOut = "aac"
			// EAC3 decoded or passed through to a receiver (Roku passes it on)
			if (acodec == "truehd" || acodec == "dts") &&
				(has(p.AudioCodecs, "eac3", canonAudio) || has(p.Passthrough, "eac3", canonAudio)) {
				audioOut = "eac3"
			}
			why = append(why, "audio "+orUnknown(acodec)+" as "+audioOut)
		}
	}
	stereo := false
	if p.StereoBeside && hasAudio {
		out := acodec
		if audioOut != "" {
			out = audioOut
		}
		stereo = dolby(out)
	}

	// 5. DASH-only codecs
	if has(p.DASHOnlyVideo, vcodec, canonVideo) {
		if !p.SupportsDASH {
			return transcode(vcodec + " plays only by DASH on this device, which takes no DASH")
		}
		return Verdict{Mode: ModeRemux, Reason: strings.Join(append(why, vcodec+" only by DASH"), "; "),
			Format: "dash", AudioCodec: audioOut, StripDV: stripDV, StereoAAC: stereo}
	}

	// 6. direct or remux
	containerOK := false
	for _, name := range strings.Split(src.Container, ",") {
		if has(p.Containers, name, canonContainer) {
			containerOK = true
			break
		}
	}
	if containerOK && !stripDV && audioOut == "" {
		return Verdict{Mode: ModeDirect, Reason: "all in profile"}
	}
	if !containerOK {
		why = append(why, "container not in profile")
	}
	if strings.EqualFold(ov.Format, "dash") && p.SupportsDASH {
		return Verdict{Mode: ModeRemux, Reason: strings.Join(why, "; "), Format: "dash",
			AudioCodec: audioOut, StripDV: stripDV, StereoAAC: stereo}
	}
	seg := segmentsFor(p, vcodec)
	if seg == "" {
		return transcode(vcodec + " in no HLS segment format the device plays")
	}
	return Verdict{Mode: ModeRemux, Reason: strings.Join(why, "; "), Segments: seg,
		AudioCodec: audioOut, StripDV: stripDV, StereoAAC: stereo}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
