package ffmpeg

import (
	"context"
	"io/fs"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// vainfo on a J4125 (Synology DS920+, Debian 12's iHD 23.1), shortened.
const vainfoJ4125 = `libva info: VA-API version 1.17.0
libva info: Trying to open /usr/lib/x86_64-linux-gnu/dri/iHD_drv_video.so
libva info: Found init function __vaDriverInit_1_17
libva info: va_openDriver() returns 0
vainfo: VA-API version: 1.17 (libva 2.12.0)
vainfo: Driver version: Intel iHD driver for Intel(R) Gen Graphics - 23.1.1 ()
vainfo: Supported profile and entrypoints
      VAProfileNone                   :	VAEntrypointVideoProc
      VAProfileNone                   :	VAEntrypointStats
      VAProfileMPEG2Simple            :	VAEntrypointVLD
      VAProfileMPEG2Main              :	VAEntrypointVLD
      VAProfileMPEG2Main              :	VAEntrypointEncSlice
      VAProfileH264Main               :	VAEntrypointVLD
      VAProfileH264Main               :	VAEntrypointEncSlice
      VAProfileH264Main               :	VAEntrypointFEI
      VAProfileH264Main               :	VAEntrypointEncSliceLP
      VAProfileH264High               :	VAEntrypointVLD
      VAProfileH264High               :	VAEntrypointEncSlice
      VAProfileH264ConstrainedBaseline:	VAEntrypointVLD
      VAProfileVC1Advanced            :	VAEntrypointVLD
      VAProfileJPEGBaseline           :	VAEntrypointVLD
      VAProfileJPEGBaseline           :	VAEntrypointEncPicture
      VAProfileVP8Version0_3          :	VAEntrypointVLD
      VAProfileHEVCMain               :	VAEntrypointVLD
      VAProfileHEVCMain               :	VAEntrypointEncSlice
      VAProfileHEVCMain10             :	VAEntrypointVLD
      VAProfileHEVCMain10             :	VAEntrypointEncSlice
      VAProfileVP9Profile0            :	VAEntrypointVLD
      VAProfileVP9Profile2            :	VAEntrypointVLD
`

// vainfo on an AMD APU (Ryzen V1500B's sibling with graphics, Mesa 22.3).
const vainfoMesa = `libva info: VA-API version 1.17.0
libva info: Trying to open /usr/lib/x86_64-linux-gnu/dri/radeonsi_drv_video.so
libva info: Found init function __vaDriverInit_1_17
libva info: va_openDriver() returns 0
vainfo: VA-API version: 1.17 (libva 2.12.0)
vainfo: Driver version: Mesa Gallium driver 22.3.6 for AMD Radeon Vega 8 Graphics (raven, LLVM 15.0.6, DRM 3.49)
vainfo: Supported profile and entrypoints
      VAProfileMPEG2Simple            :	VAEntrypointVLD
      VAProfileMPEG2Main              :	VAEntrypointVLD
      VAProfileVC1Simple              :	VAEntrypointVLD
      VAProfileVC1Advanced            :	VAEntrypointVLD
      VAProfileH264ConstrainedBaseline:	VAEntrypointVLD
      VAProfileH264ConstrainedBaseline:	VAEntrypointEncSlice
      VAProfileH264Main               :	VAEntrypointVLD
      VAProfileH264Main               :	VAEntrypointEncSlice
      VAProfileH264High               :	VAEntrypointVLD
      VAProfileH264High               :	VAEntrypointEncSlice
      VAProfileJPEGBaseline           :	VAEntrypointVLD
      VAProfileNone                   :	VAEntrypointVideoProc
`

// T01: the J4125's iHD encodes H.264 and HEVC and decodes HEVC 10-bit and VP9.
func TestParseVAInfo_J4125(t *testing.T) {
	d := ParseVAInfo(vainfoJ4125)
	if d.Driver != "iHD" || !d.H264Enc || !d.HEVCEnc {
		t.Fatalf("got %+v", d)
	}
	want := []string{"h264", "hevc", "hevc10", "mpeg2", "vc1", "vp9", "vp9_10"}
	if !reflect.DeepEqual(d.Decode, want) {
		t.Errorf("decode %v, want %v", d.Decode, want)
	}
	if got := strings.Join(d.Codecs(), " and "); got != "H.264 and HEVC" {
		t.Errorf("codecs %q", got)
	}
}

// T02: Mesa on an AMD APU encodes H.264 only; its driver named from the library libva opened.
func TestParseVAInfo_Mesa(t *testing.T) {
	d := ParseVAInfo(vainfoMesa)
	if d.Driver != "radeonsi" || !d.H264Enc || d.HEVCEnc {
		t.Fatalf("got %+v", d)
	}
	if d.Decodes("hevc", false) || !d.Decodes("h264", false) {
		t.Errorf("decode %v", d.Decode)
	}
	// no "Trying to open" line (libva's own messages off): the version line names it
	d = ParseVAInfo("vainfo: Driver version: Intel i965 driver for Intel(R) Haswell - 2.4.1\n      VAProfileH264Main : VAEntrypointEncSlice\n")
	if d.Driver != "i965" || !d.H264Enc {
		t.Errorf("i965: %+v", d)
	}
}

func TestVAAPIDevice_Decodes(t *testing.T) {
	d := ParseVAInfo(vainfoJ4125)
	for _, c := range []struct {
		codec, pix string
		want       bool
	}{
		{"h264", "yuv420p", true},
		{"h264", "yuv420p10le", false}, // High 10: no driver decodes it
		{"hevc", "yuv420p10le", true},
		{"hevc", "yuv420p", true},
		{"vp9", "yuv420p10le", true},
		{"av1", "yuv420p", false}, // the J4125 has no AV1 decoder
		{"mpeg4", "yuv420p", false},
	} {
		if got := d.Decodes(c.codec, TenBit(c.pix)); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.codec, c.pix, got, c.want)
		}
	}
}

// probe is a VAAPIProbe over fake nodes: open's error per node, vainfo's output, the encode's
// error.
func fakeProbe(nodes []string, open map[string]error, info map[string]string, enc map[string]string) *VAAPIProbe {
	return &VAAPIProbe{
		Devices: func() []string { return nodes },
		Open:    func(p string) error { return open[p] },
		Group:   func(string) int { return 993 },
		UID:     func() int { return 1026 },
		VAInfo: func(_ context.Context, d string) (string, error) {
			if out, ok := info[d]; ok {
				return out, nil
			}
			return "vaInitialize failed with error code -1 (unknown libva error)", &fs.PathError{Op: "vainfo", Path: d, Err: syscall.EIO}
		},
		Encode: func(_ context.Context, _, d string) string { return enc[d] },
	}
}

// T05: the node is there, but the container's user may not open it: software, and the fix
// names the user and the node's group.
func TestVAAPIProbe_GroupAdd(t *testing.T) {
	p := fakeProbe([]string{"/dev/dri/renderD128"},
		map[string]error{"/dev/dri/renderD128": &fs.PathError{Op: "open", Path: "/dev/dri/renderD128", Err: syscall.EACCES}}, nil, nil)
	_, st := p.Find(context.Background(), "ffmpeg")
	if st.Encoder != "" || st.Reason != ReasonGroupAdd || st.UID != 1026 || st.GID != 993 || st.Device != "/dev/dri/renderD128" {
		t.Fatalf("got %+v", st)
	}
}

func TestVAAPIProbe_Find(t *testing.T) {
	a, b := "/dev/dri/renderD128", "/dev/dri/renderD129"
	// two nodes (a discrete card that fails, then the iGPU): the first whose encode works
	p := fakeProbe([]string{a, b}, nil, map[string]string{a: vainfoJ4125, b: vainfoJ4125},
		map[string]string{a: "Failed to initialise VAAPI connection: -1 (unknown libva error)."})
	dev, st := p.Find(context.Background(), "ffmpeg")
	if st.Encoder != "vaapi" || dev.Path != b || st.VAAPI == nil || st.VAAPI.Driver != "iHD" ||
		!reflect.DeepEqual(st.Codecs, []string{"H.264", "HEVC"}) {
		t.Fatalf("got %+v %+v", dev, st)
	}
	// the admin's pick: only it
	p.Device = a
	if _, st := p.Find(context.Background(), "ffmpeg"); st.Reason != ReasonDeviceFailed || st.Device != a ||
		!strings.Contains(st.Error, "Failed to initialise VAAPI") {
		t.Errorf("render_device: %+v", st)
	}
	// none
	if _, st := fakeProbe(nil, nil, nil, nil).Find(context.Background(), "ffmpeg"); st.Reason != ReasonNoDevice {
		t.Errorf("no node: %+v", st)
	}
	// vainfo cannot open it (a fake node): its last line is the reason
	if _, st := fakeProbe([]string{a}, nil, nil, nil).Find(context.Background(), "ffmpeg"); st.Reason != ReasonDeviceFailed ||
		!strings.HasPrefix(st.Error, "vainfo: vaInitialize failed") {
		t.Errorf("vainfo fails: %+v", st)
	}
	// a driver without H.264 encode (an old AMD card's decode-only one)
	if _, st := fakeProbe([]string{a}, nil, map[string]string{a: "      VAProfileH264Main : VAEntrypointVLD\n"}, nil).
		Find(context.Background(), "ffmpeg"); st.Reason != ReasonDeviceFailed {
		t.Errorf("decode only: %+v", st)
	}
	// vainfo not installed: the test encode decides, H.264 taken
	q := fakeProbe([]string{a}, nil, nil, nil)
	q.VAInfo = func(context.Context, string) (string, error) { return "", ErrNoVAInfo }
	if _, st := q.Find(context.Background(), "ffmpeg"); st.Encoder != "vaapi" || !reflect.DeepEqual(st.Codecs, []string{"H.264"}) {
		t.Errorf("no vainfo: %+v", st)
	}
}

// VA-API is tried after NVENC and QSV, before QSV on a NAS, and only when ffmpeg lists
// h264_vaapi; its reason is kept when everything ends in software.
func TestDetector_VAAPI(t *testing.T) {
	all := []string{"libx264", "h264_nvenc", "h264_qsv", "h264_vaapi"}
	node := "/dev/dri/renderD128"
	works := fakeProbe([]string{node}, nil, map[string]string{node: vainfoJ4125}, nil)
	for _, c := range []struct {
		name, prefer string
		nas          bool
		gpu          map[string]bool
		probe        *VAAPIProbe
		want         string
		reason       string
	}{
		{"an N100 desktop: QSV first", "", false, map[string]bool{"h264_qsv": true}, works, "qsv", ""},
		{"an N100 NAS: VA-API first", "", true, map[string]bool{"h264_qsv": true}, works, "vaapi", ""},
		{"NVIDIA stays first", "", true, map[string]bool{"h264_nvenc": true}, works, "nvenc", ""},
		{"QSV fails, VA-API works", "", false, nil, works, "vaapi", ""},
		{"no device", "", true, nil, fakeProbe(nil, nil, nil, nil), "software", ReasonNoDevice},
		{"T06: prefer vaapi, no device", "vaapi", false, map[string]bool{"h264_nvenc": true}, fakeProbe(nil, nil, nil, nil), "software", ReasonNoDevice},
		{"prefer software", "software", true, nil, works, "software", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := detector("linux", all, c.gpu)
			d.Prefer, d.PreferVAAPI, d.VAAPI = c.prefer, c.nas, c.probe
			d.Now = func() time.Time { return time.Unix(0, 0) }
			st, _, err := d.detect(context.Background())
			if err != nil || st.Encoder != c.want || st.Reason != c.reason || st.SoftwareOnly != (c.want == "software") {
				t.Fatalf("got %+v, %v; want %s (%s)", st, err, c.want, c.reason)
			}
		})
	}
	// an ffmpeg without h264_vaapi, VA-API asked for: said so
	d := detector("linux", []string{"libx264"}, nil)
	d.Prefer, d.VAAPI = "vaapi", works
	if st, _, _ := d.detect(context.Background()); st.Encoder != "software" || st.Reason != ReasonDeviceFailed {
		t.Errorf("no h264_vaapi: %+v", st)
	}
	if hw, _ := d.Detect(context.Background()); hw != HWAccelSoftware {
		t.Errorf("Detect: %q", hw)
	}
}

func TestChoice_Status(t *testing.T) {
	var nilChoice *Choice
	if st := nilChoice.Status(); st.Encoder != "software" || !st.SoftwareOnly || nilChoice.Get() != HWAccelSoftware {
		t.Errorf("nil: %+v", st)
	}
	dev := VAAPIDevice{Path: "/dev/dri/renderD128"}
	c := NewChoiceForTest(VideoStatus{Encoder: "vaapi", VAAPI: &dev})
	if c.Get() != HWAccelVAAPI || c.VAAPIDevice().Path != dev.Path {
		t.Errorf("vaapi: %q %+v", c.Get(), c.VAAPIDevice())
	}
}

// The VA-API pipeline: decode and scale on the device, h264_vaapi at every rung, no
// yuv420p pixel format (the frames are the device's nv12).
func TestHLSArgs_VAAPI(t *testing.T) {
	dev := ParseVAInfo(vainfoJ4125)
	dev.Path = "/dev/dri/renderD128"
	plan := PlanFor("vaapi", 3, 3, "hevc", true).WithVAAPI(dev, "hevc", "yuv420p10le")
	args := HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), plan)
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{
		" -vaapi_device /dev/dri/renderD128 -hwaccel vaapi -hwaccel_output_format vaapi ",
		" -c:v:0 h264_vaapi ", " -c:v:2 h264_vaapi ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q in %s", want, joined)
		}
	}
	if f := argAfter(args, "-filter:v:0"); f != vaapiScale(1080) || !strings.HasPrefix(f, "scale_vaapi=w=") || !strings.HasSuffix(f, ":format=nv12") {
		t.Errorf("rung 0 filter %q", f)
	}
	if argAfter(args, "-pix_fmt:v:0") != "" || argAfter(args, "-preset:v:0") != "" {
		t.Errorf("a VA-API rung with a pixel format or preset: %s", joined)
	}
	// the -vaapi_device comes before the input
	if i, j := strings.Index(joined, "-vaapi_device"), strings.Index(joined, " -i "); i < 0 || i > j {
		t.Errorf("device after the input: %s", joined)
	}
}

// T03: a source the device does not decode (10-bit H.264, AV1 on a J4125): the CPU decodes,
// each rung uploads its scaled frames to the device and encodes with h264_vaapi.
func TestHLSArgs_VAAPISoftwareDecode(t *testing.T) {
	dev := ParseVAInfo(vainfoJ4125)
	dev.Path = "/dev/dri/renderD128"
	for _, c := range []struct{ codec, pix string }{{"h264", "yuv420p10le"}, {"av1", "yuv420p"}} {
		plan := PlanFor("vaapi", 3, 3, c.codec, true).WithVAAPI(dev, c.codec, c.pix)
		args := HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), plan)
		if argAfter(args, "-hwaccel") != "" || argAfter(args, "-vaapi_device") != dev.Path {
			t.Errorf("%s: %v", c.codec, args)
		}
		if f := argAfter(args, "-filter:v:1"); f != FitScale(720)+",format=nv12,hwupload" {
			t.Errorf("%s: rung 1 filter %q", c.codec, f)
		}
		if argAfter(args, "-c:v:1") != "h264_vaapi" {
			t.Errorf("%s: rung 1 encoder %q", c.codec, argAfter(args, "-c:v:1"))
		}
	}
}

// T04: a burned-in subtitle is drawn on the CPU: the device's frames come down for it and go
// back up to be scaled and encoded there.
func TestHLSArgs_VAAPIBurnIn(t *testing.T) {
	dev := ParseVAInfo(vainfoJ4125)
	dev.Path = "/dev/dri/renderD128"
	plan := PlanFor("vaapi", 3, 3, "h264", true).WithVAAPI(dev, "h264", "yuv420p")
	plan.Burn = "subtitles=/subs/ar.srt"
	args := HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), plan)
	want := "hwdownload,format=nv12,subtitles=/subs/ar.srt,format=nv12,hwupload," + vaapiScale(1080)
	if f := argAfter(args, "-filter:v:0"); f != want {
		t.Errorf("burn-in filter\n got %s\nwant %s", f, want)
	}
	// software frames (the CPU decodes): the overlay, the scale, then up
	plan = PlanFor("vaapi", 3, 3, "av1", true).WithVAAPI(dev, "av1", "yuv420p")
	plan.Burn = "subtitles=/subs/ar.srt"
	if f := argAfter(HLSArgs("/in.mkv", "/out", "m.m3u8", DefaultLadder(0), plan), "-filter:v:0"); f != "subtitles=/subs/ar.srt,"+FitScale(1080)+",format=nv12,hwupload" {
		t.Errorf("software-decoded burn-in %q", f)
	}
}

// Rungs past the GPU's sessions run on libx264: frames on the device come down after its
// scaler; an HDR source is tone-mapped on the CPU, uploaded for the VA-API rungs.
func TestHLSArgs_VAAPIMixed(t *testing.T) {
	dev := ParseVAInfo(vainfoJ4125)
	dev.Path = "/dev/dri/renderD128"
	plan := PlanFor("vaapi", 1, 3, "hevc", true).WithVAAPI(dev, "hevc", "yuv420p")
	args := HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), plan)
	if f := argAfter(args, "-filter:v:2"); f != vaapiScale(480)+",hwdownload,format=nv12" {
		t.Errorf("libx264 rung %q", f)
	}
	if argAfter(args, "-pix_fmt:v:2") != "yuv420p" {
		t.Errorf("libx264 rung without yuv420p")
	}
	hdr := PlanFor("vaapi", 3, 3, "hevc", true)
	hdr.ToneMap = ToneMapFilters(Colour{Transfer: "smpte2084"}, 1920, 1080)
	hdr = hdr.WithVAAPI(dev, "hevc", "yuv420p10le")
	if hdr.HWDecode {
		t.Fatal("a tone-mapped source decoded on the device")
	}
	g := argAfter(HLSArgs("/in.mkv", "/out", "master.m3u8", DefaultLadder(0), hdr), "-filter_complex")
	if !strings.Contains(g, "[s0]"+FitScale(1080)+",format=nv12,hwupload[v0]") {
		t.Errorf("tone-mapped graph %q", g)
	}
}

func TestVAAPITestArgs(t *testing.T) {
	got := strings.Join(VAAPITestArgs("/dev/dri/renderD128"), " ")
	for _, want := range []string{"-vaapi_device /dev/dri/renderD128", "testsrc2=s=1920x1080:r=25:d=2", "hwupload", "-c:v h264_vaapi"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
}
