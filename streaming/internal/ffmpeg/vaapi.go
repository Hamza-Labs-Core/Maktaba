package ffmpeg

// VA-API (Epic 59 story 59.05): the video path that works on a NAS's chip. Most NAS boxes give
// a container only /dev/dri: a J4125 or an N100 (Intel's iHD driver), older Intel (i965), an
// AMD APU (Mesa). The device is the first /dev/dri/renderD* that this user can open, that
// vainfo opens and lists an H.264 encode profile for, and whose test encode (2 s of 1080p from
// lavfi) works; its profiles say which codecs it encodes and decodes.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// VAAPIDevice is a render node VA-API works on, as vainfo lists it.
type VAAPIDevice struct {
	Path string `json:"device"`
	// Driver is the VA driver's name (iHD, i965, radeonsi), "" when vainfo does not say.
	Driver string `json:"driver,omitempty"`
	// H264Enc and HEVCEnc: the device encodes those (an EncSlice entry point).
	H264Enc bool `json:"h264_enc"`
	HEVCEnc bool `json:"hevc_enc"`
	// Decode are the codecs it decodes (a VLD entry point): h264, hevc, hevc10, vp9, vp9_10,
	// av1, mpeg2, vc1.
	Decode []string `json:"decode"`
}

// Codecs are the codecs the device encodes, for Settings ("H.264 and HEVC").
func (d VAAPIDevice) Codecs() []string {
	var out []string
	if d.H264Enc {
		out = append(out, "H.264")
	}
	if d.HEVCEnc {
		out = append(out, "HEVC")
	}
	return out
}

// Decodes reports whether the device decodes a source of codec (the probe's name) at its bit
// depth (tenBit: a 10-bit pixel format).
func (d VAAPIDevice) Decodes(codec string, tenBit bool) bool {
	want := ""
	switch strings.ToLower(codec) {
	case "h264", "avc", "avc1":
		if tenBit { // High 10: no VA-API driver decodes it
			return false
		}
		want = "h264"
	case "hevc", "h265", "hev1", "hvc1":
		want = "hevc"
	case "vp9":
		want = "vp9"
	case "av1":
		want = "av1"
	case "mpeg2video", "mpeg2":
		want = "mpeg2"
	case "vc1", "wmv3":
		want = "vc1"
	default:
		return false
	}
	if tenBit { // AV1's Main profile is 10-bit too
		switch want {
		case "mpeg2", "vc1":
			return false
		case "hevc":
			want = "hevc10"
		case "vp9":
			want = "vp9_10"
		}
	}
	for _, c := range d.Decode {
		if c == want {
			return true
		}
	}
	return false
}

// TenBit reports whether a pixel format (ffprobe's name) has more than 8 bits a sample.
func TenBit(pixFmt string) bool {
	p := strings.ToLower(pixFmt)
	return strings.Contains(p, "10") || strings.Contains(p, "12") || strings.HasPrefix(p, "p010") || strings.HasPrefix(p, "p016")
}

// vaDecode maps vainfo's decode profiles to the codec names Decode lists.
var vaDecode = map[string]string{
	"VAProfileH264ConstrainedBaseline": "h264",
	"VAProfileH264Main":                "h264",
	"VAProfileH264High":                "h264",
	"VAProfileHEVCMain":                "hevc",
	"VAProfileHEVCMain10":              "hevc10",
	"VAProfileVP9Profile0":             "vp9",
	"VAProfileVP9Profile2":             "vp9_10",
	"VAProfileAV1Profile0":             "av1",
	"VAProfileMPEG2Main":               "mpeg2",
	"VAProfileMPEG2Simple":             "mpeg2",
	"VAProfileVC1Advanced":             "vc1",
	"VAProfileVC1Main":                 "vc1",
}

// ParseVAInfo reads `vainfo --display drm --device <d>`'s output: the driver (the library
// libva opened, else the driver version line) and its profiles' entry points.
func ParseVAInfo(out string) VAAPIDevice {
	var d VAAPIDevice
	dec := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.Contains(l, "Trying to open ") && strings.HasSuffix(l, "_drv_video.so"):
			d.Driver = strings.TrimSuffix(filepath.Base(l[strings.LastIndex(l, " ")+1:]), "_drv_video.so")
		case strings.HasPrefix(l, "vainfo: Driver version:") && d.Driver == "":
			v := strings.TrimSpace(strings.TrimPrefix(l, "vainfo: Driver version:"))
			switch {
			case strings.Contains(v, "iHD"):
				d.Driver = "iHD"
			case strings.Contains(v, "i965"):
				d.Driver = "i965"
			case strings.HasPrefix(v, "Mesa"):
				d.Driver = "radeonsi"
			}
		case strings.HasPrefix(l, "VAProfile"):
			profile, entry, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			profile, entry = strings.TrimSpace(profile), strings.TrimSpace(entry)
			enc := strings.HasPrefix(entry, "VAEntrypointEncSlice")
			switch {
			case enc && strings.HasPrefix(profile, "VAProfileH264"):
				d.H264Enc = true
			case enc && strings.HasPrefix(profile, "VAProfileHEVCMain"):
				d.HEVCEnc = true
			case entry == "VAEntrypointVLD":
				if c := vaDecode[profile]; c != "" {
					dec[c] = true
				}
			}
		}
	}
	for c := range dec {
		d.Decode = append(d.Decode, c)
	}
	sort.Strings(d.Decode)
	return d
}

// The video verdict's reasons (Settings → This machine: machine.video.<reason>).
const (
	ReasonNoDevice     = "no_device"     // no render node in this container
	ReasonGroupAdd     = "group_add"     // there, but this user cannot open it
	ReasonDeviceFailed = "device_failed" // it opened, but could not encode
)

// VideoStatus is the transcodes' encoder verdict and why, as Settings shows it.
type VideoStatus struct {
	// Encoder is the verdict: vaapi, qsv, nvenc, videotoolbox or software.
	Encoder string `json:"encoder"`
	// VAAPI is the render node VA-API runs on (Encoder vaapi).
	VAAPI *VAAPIDevice `json:"vaapi,omitempty"`
	// Codecs it encodes (H.264, HEVC).
	Codecs []string `json:"codecs,omitempty"`
	// Reason is why the video is in software ("" none or not asked): no_device, group_add,
	// device_failed; Device the render node it names, Error what failed, UID the user that
	// could not open it and GID the device's group.
	Reason string `json:"reason,omitempty"`
	Device string `json:"device,omitempty"`
	Error  string `json:"error,omitempty"`
	UID    int    `json:"uid,omitempty"`
	GID    int    `json:"gid,omitempty"`
	// SoftwareOnly: no hardware encoder works, every transcode is in software.
	SoftwareOnly bool      `json:"software_only"`
	CheckedAt    time.Time `json:"checked_at"`
}

// VAAPIProbe finds the render node VA-API works on (hooks for the tests).
type VAAPIProbe struct {
	// Device is the admin's pick ([streaming] render_device): only it is tried.
	Device string
	// Devices lists the render nodes (/dev/dri/renderD*), sorted.
	Devices func() []string
	// Open opens a node for reading and writing (VA-API's open), its error as the system's.
	Open func(path string) error
	// Group is the node's group id (-1: unknown).
	Group func(path string) int
	// UID is this process's user.
	UID func() int
	// VAInfo runs vainfo on a node; ErrNoVAInfo when it is not installed.
	VAInfo func(ctx context.Context, device string) (string, error)
	// Encode test-encodes 2 s of 1080p with h264_vaapi on a node: "" it worked, else ffmpeg's error.
	Encode func(ctx context.Context, ffmpeg, device string) string
}

// ErrNoVAInfo: vainfo is not installed (an install outside the image): the test encode alone
// decides, with H.264 taken as the codec.
var ErrNoVAInfo = errors.New("vainfo is not installed")

// DefaultVAAPIProbe is the probe on this machine.
func DefaultVAAPIProbe(device string) *VAAPIProbe {
	return &VAAPIProbe{
		Device:  device,
		Devices: renderNodes,
		Open: func(path string) error {
			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err == nil {
				_ = f.Close()
			}
			return err
		},
		Group:  nodeGroup,
		UID:    os.Getuid,
		VAInfo: vainfo,
		Encode: vaapiEncodes,
	}
}

func renderNodes() []string {
	n, _ := filepath.Glob("/dev/dri/renderD*")
	sort.Strings(n)
	return n
}

// Find tries each render node in turn: the first that opens, that vainfo lists an H.264
// encode for and whose test encode works. Otherwise the reason of the first node that failed
// (no_device when there is none).
func (p *VAAPIProbe) Find(ctx context.Context, ffmpeg string) (VAAPIDevice, VideoStatus) {
	nodes := []string{}
	if p.Device != "" {
		nodes = append(nodes, p.Device)
	} else if p.Devices != nil {
		nodes = p.Devices()
	}
	var first *VideoStatus
	fail := func(s VideoStatus) {
		if first == nil {
			first = &s
		}
	}
	for _, n := range nodes {
		if err := p.Open(n); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
				gid := -1
				if p.Group != nil {
					gid = p.Group(n)
				}
				fail(VideoStatus{Reason: ReasonGroupAdd, Device: n, UID: p.UID(), GID: gid, Error: err.Error()})
				continue
			}
			fail(VideoStatus{Reason: ReasonDeviceFailed, Device: n, Error: err.Error()})
			continue
		}
		dev := VAAPIDevice{Path: n, H264Enc: true}
		out, err := p.VAInfo(ctx, n)
		switch {
		case errors.Is(err, ErrNoVAInfo):
		case err != nil:
			fail(VideoStatus{Reason: ReasonDeviceFailed, Device: n, Error: "vainfo: " + lastLine(out, err)})
			continue
		default:
			dev = ParseVAInfo(out)
			dev.Path = n
			if !dev.H264Enc {
				fail(VideoStatus{Reason: ReasonDeviceFailed, Device: n, Error: "the driver lists no H.264 encode profile"})
				continue
			}
		}
		if e := p.Encode(ctx, ffmpeg, n); e != "" {
			fail(VideoStatus{Reason: ReasonDeviceFailed, Device: n, Error: e})
			continue
		}
		return dev, VideoStatus{Encoder: string(HWAccelVAAPI), VAAPI: &dev, Codecs: dev.Codecs()}
	}
	if first == nil {
		return VAAPIDevice{}, VideoStatus{Reason: ReasonNoDevice}
	}
	return VAAPIDevice{}, *first
}

// lastLine is a failed command's last line of output, else its error.
func lastLine(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

func vainfo(ctx context.Context, device string) (string, error) {
	bin, err := exec.LookPath("vainfo")
	if err != nil {
		return "", ErrNoVAInfo
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--display", "drm", "--device", device).CombinedOutput()
	return string(out), err
}

// VAAPITestArgs are the test encode's arguments: 2 s of 1080p from lavfi, uploaded to the
// device and encoded with h264_vaapi, as a transcode's top rung is.
func VAAPITestArgs(device string) []string {
	return []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-vaapi_device", device,
		"-f", "lavfi", "-i", "testsrc2=s=1920x1080:r=25:d=2",
		"-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-f", "null", "-"}
}

func vaapiEncodes(ctx context.Context, ffmpeg, device string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, VAAPITestArgs(device)...).CombinedOutput()
	if err == nil {
		return ""
	}
	return lastLine(string(out), err)
}
