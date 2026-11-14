package capability

// tvProfiles are Epic 58's TV platforms (story 58.02; research.md §2). Each
// profile is a conservative baseline that plays on every model of its platform
// (H.264 1080p in HLS TS), and its Ceiling the most any model of the platform
// plays: a device's report (DeviceCaps) widens the baseline only up to it.
//
//   - firetv: HEVC/AV1 2160p, HDR10/HLG, Dolby Vision 5 and 8, EAC3/TrueHD
//     passthrough, MKV direct.
//   - tizen (Samsung): HEVC/AV1 2160p (8K is never asked), HDR10/HLG, never
//     Dolby Vision (Samsung has none), MKV direct through AVPlay.
//   - webos (LG): HEVC/AV1 2160p, HDR10/HLG, Dolby Vision 8 only through its
//     HDR10 base, HLS fMP4, MKV direct.
//   - roku: HEVC 2160p, HDR10/HLG, Dolby Vision 5 only, AV1 only by DASH,
//     AC3/EAC3 passthrough (no TrueHD), MKV direct, a stereo AAC track beside
//     any Dolby one.
//   - cast: HEVC/AV1 2160p in HLS fMP4 (never TS), HDR10/HLG/Dolby Vision where
//     the receiver's canDisplayType says so, no MKV.
//   - vega (Vega OS): HEVC/AV1 2160p, HDR10/HLG, EAC3/AC4, MKV direct.
func tvProfiles() []*Profile {
	baseline := func(name string, audio []string) *Profile {
		return &Profile{
			Name:             name,
			Containers:       []string{"mp4"},
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      audio,
			HDR:              []string{"sdr"},
			MaxHeight:        1080,
			MaxBitrateKbps:   20000,
			MaxAudioChannels: 6,
			SupportsHLS:      true,
			HLSSegments:      []string{"ts"},
			Detailed:         true,
		}
	}
	dolbyAAC := []string{"aac", "ac3", "eac3"}

	firetv := baseline("firetv", dolbyAAC)
	firetv.Ceiling = &Profile{
		Containers:       []string{"mp4", "mkv", "mov", "ts", "webm"},
		VideoCodecs:      []string{"h264", "hevc", "av1", "vp9"},
		AudioCodecs:      []string{"aac", "ac3", "eac3", "opus", "flac", "mp3"},
		Passthrough:      []string{"ac3", "eac3", "truehd", "dts"},
		HDR:              []string{"sdr", "hdr10", "hlg", "dv-p5", "dv-p8"},
		MaxHeight:        2160,
		MaxBitrateKbps:   100000,
		MaxAudioChannels: 8,
		SupportsHLS:      true,
		SupportsDASH:     true,
		HLSSegments:      []string{"ts", "fmp4"},
	}

	tizen := baseline("tizen", dolbyAAC)
	tizen.Ceiling = &Profile{
		Containers:       []string{"mp4", "mkv", "mov", "ts"},
		VideoCodecs:      []string{"h264", "hevc", "av1", "vp9"},
		AudioCodecs:      []string{"aac", "ac3", "eac3", "mp3"},
		Passthrough:      []string{"ac3", "eac3"},
		HDR:              []string{"sdr", "hdr10", "hlg"}, // Samsung never gets Dolby Vision
		MaxHeight:        2160,
		MaxBitrateKbps:   80000,
		MaxAudioChannels: 8,
		SupportsHLS:      true,
		SupportsDASH:     true,
		HLSSegments:      []string{"ts", "fmp4"},
	}

	webos := baseline("webos", dolbyAAC)
	webos.Ceiling = &Profile{
		Containers:       []string{"mp4", "mkv", "mov", "ts"},
		VideoCodecs:      []string{"h264", "hevc", "av1", "vp9"},
		AudioCodecs:      []string{"aac", "ac3", "eac3", "mp3"},
		Passthrough:      []string{"ac3", "eac3"},
		HDR:              []string{"sdr", "hdr10", "hlg"}, // Dolby Vision 8 through its HDR10 base
		MaxHeight:        2160,
		MaxBitrateKbps:   80000,
		MaxAudioChannels: 8,
		SupportsHLS:      true,
		SupportsDASH:     true,
		HLSSegments:      []string{"ts", "fmp4"},
	}

	roku := baseline("roku", []string{"aac"})
	roku.StereoBeside = true
	roku.Ceiling = &Profile{
		Containers:       []string{"mp4", "mkv", "mov", "ts"},
		VideoCodecs:      []string{"h264", "hevc", "av1", "vp9"},
		DASHOnlyVideo:    []string{"av1"},
		AudioCodecs:      []string{"aac", "ac3", "eac3", "mp3", "flac"},
		Passthrough:      []string{"ac3", "eac3"}, // no TrueHD
		HDR:              []string{"sdr", "hdr10", "hlg", "dv-p5"},
		MaxHeight:        2160,
		MaxBitrateKbps:   60000,
		MaxAudioChannels: 8,
		SupportsHLS:      true,
		SupportsDASH:     true,
		HLSSegments:      []string{"ts", "fmp4"},
	}
	roku.DASHOnlyVideo = roku.Ceiling.DASHOnlyVideo

	cast := baseline("cast", []string{"aac"})
	cast.Ceiling = &Profile{
		Containers:       []string{"mp4"}, // no MKV
		VideoCodecs:      []string{"h264", "hevc", "av1", "vp9"},
		AudioCodecs:      []string{"aac", "ac3", "eac3", "opus", "mp3", "flac"},
		Passthrough:      []string{"ac3", "eac3"},
		HDR:              []string{"sdr", "hdr10", "hlg", "dv-p5", "dv-p8"},
		MaxHeight:        2160,
		MaxBitrateKbps:   60000,
		MaxAudioChannels: 8,
		SupportsHLS:      true,
		SupportsDASH:     true,
		HLSSegments:      []string{"ts", "fmp4"}, // HEVC/AV1 only in fMP4 (segmentsFor)
	}

	vega := baseline("vega", []string{"aac"})
	vega.Ceiling = &Profile{
		Containers:       []string{"mp4", "mkv", "mov", "ts"},
		VideoCodecs:      []string{"h264", "hevc", "av1", "vp9"},
		AudioCodecs:      []string{"aac", "ac3", "eac3", "ac4", "mp3"},
		Passthrough:      []string{"ac3", "eac3", "ac4"},
		HDR:              []string{"sdr", "hdr10", "hlg"},
		MaxHeight:        2160,
		MaxBitrateKbps:   80000,
		MaxAudioChannels: 8,
		SupportsHLS:      true,
		SupportsDASH:     true,
		HLSSegments:      []string{"ts", "fmp4"},
	}

	out := []*Profile{firetv, tizen, webos, roku, cast, vega}
	for _, p := range out {
		p.Ceiling.Name = p.Name
		p.Ceiling.Detailed = true
		p.Ceiling.StereoBeside = p.StereoBeside
	}
	return out
}
