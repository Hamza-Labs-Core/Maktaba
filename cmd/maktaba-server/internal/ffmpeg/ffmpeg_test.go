package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func fixture(t *testing.T, version, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "ffmpeg-"+version, name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// FFmpeg 8.1's listings (the Windows payload's build) have all Maktaba asks.
func TestListingsOf81(t *testing.T) {
	enc := parseCodecs(fixture(t, "8.1", "encoders"))
	dec := parseCodecs(fixture(t, "8.1", "decoders"))
	fil := parseFilters(fixture(t, "8.1", "filters"))
	fmts := parseFormats(fixture(t, "8.1", "formats"))
	pro := parseProtocols(fixture(t, "8.1", "protocols"))
	for _, c := range []struct {
		have   map[string]bool
		prefix string
		want   []string
	}{
		{enc, "", Encoders}, {dec, "", Decoders}, {fil, "", Filters},
		{fmts, "E:", Muxers}, {fmts, "D:", Demuxers}, {pro, "in:", Protocols},
	} {
		for _, w := range c.want {
			if !c.have[c.prefix+w] {
				t.Errorf("%s%s not found", c.prefix, w)
			}
		}
	}
	// not taken from a legend or a heading
	for _, bad := range []string{"=", "Encoders:", "------", "V.....", "A"} {
		if enc[bad] {
			t.Errorf("encoders has %q", bad)
		}
	}
	for _, bad := range []string{"E:lavfi", "D:null", "D:d", "D:3dostr,"} {
		if fmts[bad] {
			t.Errorf("formats has %q", bad)
		}
	}
	if !fmts["D:mp4"] || !fmts["D:webm"] || !fmts["D:dshow"] {
		t.Error("a demuxer of several names, or a device, is missing")
	}
	if !pro["in:https"] || !pro["out:file"] || pro["in:Input:"] {
		t.Errorf("protocols: %v", pro)
	}
	if v := parseVersion(fixture(t, "8.1", "version")); v != "8.1.2-essentials_build-www.gyan.dev" {
		t.Errorf("version %q", v)
	}
}

// FFmpeg 4.4 lists formats with two flag columns and filters with three.
func TestListingsOf44(t *testing.T) {
	fmts := parseFormats(fixture(t, "4.4", "formats"))
	for _, w := range Muxers {
		if !fmts["E:"+w] {
			t.Errorf("muxer %s not found", w)
		}
	}
	for _, w := range Demuxers {
		if !fmts["D:"+w] {
			t.Errorf("demuxer %s not found", w)
		}
	}
	if fmts["E:lavfi"] || !fmts["E:chromaprint"] || fmts["D:chromaprint"] {
		t.Errorf("flags misread: %v", fmts)
	}
	fil := parseFilters(fixture(t, "4.4", "filters"))
	for _, w := range Filters {
		if !fil[w] {
			t.Errorf("filter %s not found", w)
		}
	}
	if fil["="] || fil["Audio"] || len(fil) != len(Filters) {
		t.Errorf("filters: %v", fil)
	}
}

func TestNumericVersion(t *testing.T) {
	for v, want := range map[string][2]int{
		"4.4.2-0ubuntu0.22.04.1": {4, 4}, "5.1.6-0+deb12u1": {5, 1}, "n7.1": {7, 1}, "6.1.2": {6, 1},
		"8.1.2-essentials_build-www.gyan.dev": {8, 1},
	} {
		if got, ok := numericVersion(v); !ok || got != want {
			t.Errorf("%s: %v %v", v, got, ok)
		}
	}
	for _, v := range []string{"N-115000-g1a2b3c4d5e", "", "git-2024-01-01"} {
		if _, ok := numericVersion(v); ok {
			t.Errorf("%s has a release number", v)
		}
	}
}

// fakeFFmpeg answers as ffmpeg 8.1 would, changed by edit (the listing's arg, then
// its text), and records the runs.
type fakeFFmpeg struct {
	t       *testing.T
	version string
	edit    func(arg, out string) string
	fail    map[string]error // by tool path + " " + first arg past -hide_banner
	runs    []string
}

func (f *fakeFFmpeg) run(_ context.Context, name string, args ...string) (string, error) {
	arg := ""
	for _, a := range args {
		if a != "-hide_banner" && a != "-nostdin" {
			arg = a
			break
		}
	}
	f.runs = append(f.runs, filepath.Base(name)+" "+arg)
	if err := f.fail[filepath.Base(name)+" "+arg]; err != nil {
		return "", err
	}
	if strings.Contains(filepath.Base(name), "ffprobe") {
		return "ffprobe version " + f.version + " Copyright\n", nil
	}
	var out string
	switch arg {
	case "-version":
		out = "ffmpeg version " + f.version + " Copyright (c) 2000-2026 the FFmpeg developers\n"
	case "-encoders", "-decoders", "-filters", "-formats", "-protocols":
		out = fixture(f.t, "8.1", strings.TrimPrefix(arg, "-"))
	default: // the test encode
		out = ""
	}
	if f.edit != nil {
		out = f.edit(arg, out)
	}
	return out, nil
}

// without drops the listing's lines naming name (its own column).
func without(listing string, names ...string) func(arg, out string) string {
	return func(arg, out string) string {
		if arg != listing {
			return out
		}
		var keep []string
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if slices.ContainsFunc(names, func(n string) bool { return slices.Contains(f, n) }) {
				continue
			}
			keep = append(keep, l)
		}
		return strings.Join(keep, "\n")
	}
}

func fake(t *testing.T, f *fakeFFmpeg, rel OSRelease) {
	t.Helper()
	f.t = t
	if f.version == "" {
		f.version = "6.1.2"
	}
	prevRun, prevOS, prevGOOS := run, osRelease, goos
	run, osRelease, goos = f.run, func() OSRelease { return rel }, "linux"
	t.Cleanup(func() { run, osRelease, goos = prevRun, prevOS, prevGOOS })
}

var (
	ubuntu = OSRelease{ID: "ubuntu", IDLike: "debian", VersionID: "22.04", PrettyName: "Ubuntu 22.04.5 LTS"}
	fedora = OSRelease{ID: "fedora", VersionID: "40", PrettyName: "Fedora Linux 40 (Container Image)"}
	both   = Tools{FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", Source: FromPath}
)

func TestCheckOK(t *testing.T) {
	f := &fakeFFmpeg{}
	fake(t, f, ubuntu)
	r := Check(context.Background(), both)
	if !r.OK || r.Problem != nil || r.Install != nil || r.Version != "6.1.2" {
		t.Fatalf("report %+v", r)
	}
	if !slices.Contains(r.HWEncoders, "h264_nvenc") {
		t.Errorf("hardware encoders %v", r.HWEncoders)
	}
	// the gyan.dev essentials build has no chromaprint
	if len(r.Warnings) != 1 || r.Warnings[0].Code != "ffmpeg_no_chromaprint" {
		t.Errorf("warnings %+v", r.Warnings)
	}
	if last := f.runs[len(f.runs)-1]; last != "ffmpeg -loglevel" {
		t.Errorf("the test encode is the last run: %v", f.runs)
	}
}

// Fedora's ffmpeg-free has no libx264: the check says so and gives RPM Fusion's.
func TestCheckFedoraFree(t *testing.T) {
	fake(t, &fakeFFmpeg{version: "6.1.2", edit: without("-encoders", "libx264", "libx264rgb")}, fedora)
	r := Check(context.Background(), both)
	if r.OK || r.Problem.Code != "ffmpeg_lacks" || r.Problem.Params["missing"] != "encoder libx264" {
		t.Fatalf("problem %+v", r.Problem)
	}
	if r.Install == nil || r.Install.Code != "ffmpeg_install" ||
		r.Install.Params["command"] != "sudo dnf install https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-40.noarch.rpm && sudo dnf install --allowerasing ffmpeg" ||
		r.Install.Params["distro"] != fedora.PrettyName {
		t.Errorf("install %+v", r.Install)
	}
}

func TestCheckProblems(t *testing.T) {
	broken := errors.New("exit status 127: error while loading shared libraries: libavdevice.so.58")
	for _, c := range []struct {
		name   string
		tools  Tools
		f      fakeFFmpeg
		code   string
		params map[string]any
		advice string // the install note's code
	}{
		{"none on the PATH", Tools{Source: FromPath}, fakeFFmpeg{}, "ffmpeg_missing", nil, "ffmpeg_install"},
		{"a setting naming nothing", Tools{Source: FromConfig, Setting: "[media] ffmpeg", Wanted: "/opt/ff/ffmpeg"},
			fakeFFmpeg{}, "ffmpeg_not_found", map[string]any{"setting": "[media] ffmpeg", "path": "/opt/ff/ffmpeg"}, ""},
		{"ffprobe's setting naming nothing", Tools{FFmpeg: "/usr/bin/ffmpeg", Source: FromPath, Setting: "MAKTABA_FFPROBE", Wanted: "/x/ffprobe"},
			fakeFFmpeg{}, "ffmpeg_not_found", map[string]any{"setting": "MAKTABA_FFPROBE", "path": "/x/ffprobe"}, ""},
		{"no ffprobe", Tools{FFmpeg: "/usr/bin/ffmpeg", Source: FromPath}, fakeFFmpeg{}, "ffprobe_missing",
			map[string]any{"path": "/usr/bin/ffmpeg"}, "ffmpeg_install"},
		// apt would install the same one again: a newer release, or another ffmpeg
		{"Ubuntu 20.04's", both, fakeFFmpeg{version: "4.2.7-0ubuntu0.1"}, "ffmpeg_old",
			map[string]any{"have": "4.2.7-0ubuntu0.1", "need": "4.4", "path": "/usr/bin/ffmpeg"}, "ffmpeg_old_distro"},
		{"no speechnorm, no lavfi", both, fakeFFmpeg{edit: func(arg, out string) string {
			return without("-formats", "lavfi")(arg, without("-filters", "speechnorm")(arg, out))
		}}, "ffmpeg_lacks", map[string]any{"missing": "filter speechnorm, demuxer lavfi", "have": "6.1.2", "path": "/usr/bin/ffmpeg"}, "ffmpeg_install"},
		{"does not start", both, fakeFFmpeg{fail: map[string]error{"ffmpeg -version": broken}}, "ffmpeg_broken",
			map[string]any{"path": "/usr/bin/ffmpeg", "error": broken.Error()}, "ffmpeg_install"},
		{"cannot encode", both, fakeFFmpeg{fail: map[string]error{"ffmpeg -loglevel": errors.New("exit status 1: Unknown encoder 'libx264'")}},
			"ffmpeg_broken", map[string]any{"path": "/usr/bin/ffmpeg", "error": "exit status 1: Unknown encoder 'libx264'"}, "ffmpeg_install"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := c.f
			fake(t, &f, ubuntu)
			r := Check(context.Background(), c.tools)
			if r.OK || r.Problem == nil || r.Problem.Code != c.code {
				t.Fatalf("problem %+v", r.Problem)
			}
			for k, v := range c.params {
				if r.Problem.Params[k] != v {
					t.Errorf("%s = %v, want %v", k, r.Problem.Params[k], v)
				}
			}
			switch {
			case c.advice == "" && r.Install != nil:
				t.Errorf("install %+v", r.Install)
			case c.advice != "" && (r.Install == nil || r.Install.Code != c.advice):
				t.Errorf("install %+v", r.Install)
			case c.advice == "ffmpeg_install" && r.Install.Params["command"] != "sudo apt install ffmpeg",
				c.advice == "ffmpeg_old_distro" && (r.Install.Params["need"] != "4.4" || r.Install.Params["distro"] != ubuntu.PrettyName):
				t.Errorf("install %+v", r.Install)
			}
		})
	}
}

// A build from git has no release number: what it lists decides.
func TestCheckGitBuild(t *testing.T) {
	fake(t, &fakeFFmpeg{version: "N-115000-g1a2b3c4d5e"}, ubuntu)
	if r := Check(context.Background(), both); !r.OK {
		t.Fatalf("report %+v", r.Problem)
	}
}

// Off Linux a problem comes without an install command (the app carries its ffmpeg).
func TestCheckOffLinux(t *testing.T) {
	fake(t, &fakeFFmpeg{}, OSRelease{})
	goos = "darwin"
	r := Check(context.Background(), Tools{Source: FromApp})
	if r.OK || r.Problem.Code != "ffmpeg_missing" || r.Install != nil {
		t.Fatalf("report %+v", r)
	}
}

func TestInstallCommand(t *testing.T) {
	const (
		apt    = "sudo apt install ffmpeg"
		pacman = "sudo pacman -S --needed ffmpeg"
	)
	el := func(v string) string {
		return "sudo dnf install https://dl.fedoraproject.org/pub/epel/epel-release-latest-" + v + ".noarch.rpm" +
			" https://mirrors.rpmfusion.org/free/el/rpmfusion-free-release-" + v + ".noarch.rpm" +
			" && sudo crb enable && sudo dnf install --allowerasing ffmpeg"
	}
	fed := func(v string) string {
		return "sudo dnf install https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-" + v + ".noarch.rpm" +
			" && sudo dnf install --allowerasing ffmpeg"
	}
	packman := func(dir string) string {
		return "sudo zypper addrepo -cfp 90 https://ftp.gwdg.de/pub/linux/misc/packman/suse/" + dir + "/ packman" +
			" && sudo zypper --gpg-auto-import-keys refresh && sudo zypper dist-upgrade --from packman --allow-vendor-change" +
			" && sudo zypper install --from packman ffmpeg"
	}
	for _, c := range []struct {
		r    OSRelease
		want string
	}{
		{ubuntu, apt},
		{OSRelease{ID: "debian", VersionID: "12"}, apt},
		{OSRelease{ID: "linuxmint", IDLike: "ubuntu debian", VersionID: "22"}, apt},
		{OSRelease{ID: "pop", IDLike: "ubuntu debian", VersionID: "24.04"}, apt},
		{fedora, fed("40")},
		{OSRelease{ID: "fedora", VersionID: "43"}, fed("43")},
		{OSRelease{ID: "nobara", IDLike: "rhel centos fedora", VersionID: "41"}, fed("41")},
		{OSRelease{ID: "rhel", IDLike: "fedora", VersionID: "9.4"}, el("9")},
		{OSRelease{ID: "rocky", IDLike: "rhel centos fedora", VersionID: "9.5"}, el("9")},
		{OSRelease{ID: "almalinux", IDLike: "rhel centos fedora", VersionID: "10.0"}, el("10")},
		{OSRelease{ID: "centos", IDLike: "rhel fedora", VersionID: "10"}, el("10")},
		{OSRelease{ID: "opensuse-tumbleweed", IDLike: "opensuse suse", VersionID: "20261001"}, packman("openSUSE_Tumbleweed")},
		{OSRelease{ID: "opensuse-slowroll", IDLike: "opensuse suse", VersionID: "20261001"}, packman("openSUSE_Slowroll")},
		{OSRelease{ID: "opensuse-leap", IDLike: "suse opensuse", VersionID: "16.0"}, packman("openSUSE_Leap_16.0")},
		{OSRelease{ID: "arch"}, pacman},
		{OSRelease{ID: "manjaro", IDLike: "arch"}, pacman},
		{OSRelease{ID: "endeavouros", IDLike: "arch"}, pacman},
		{OSRelease{ID: "gentoo"}, ""},
		// Amazon Linux says "fedora" with its year: RPM Fusion has nothing for it
		{OSRelease{ID: "amzn", IDLike: "fedora", VersionID: "2023"}, ""},
		{OSRelease{ID: "sles", IDLike: "suse", VersionID: "15.6"}, ""},
		{OSRelease{}, ""},
	} {
		if got := InstallCommand(c.r); got != c.want {
			t.Errorf("%+v:\n got %q\nwant %q", c.r, got, c.want)
		}
	}
}

// An unknown distribution gets what to look for instead of a command.
func TestInstallNoteOther(t *testing.T) {
	prev := goos
	goos = "linux"
	t.Cleanup(func() { goos = prev })
	n := installNote(OSRelease{ID: "gentoo", PrettyName: "Gentoo Linux"}, "ffmpeg_lacks")
	if n == nil || n.Code != "ffmpeg_install_other" || n.Params["need"] != "4.4" {
		t.Fatalf("note %+v", n)
	}
}

// exe makes an executable file (programFile's name) in dir.
func exe(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, programFile(name))
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeEnv(t *testing.T, vars map[string]string, path map[string]string) {
	t.Helper()
	prevEnv, prevLook := getenv, lookPath
	getenv = func(k string) string { return vars[k] }
	lookPath = func(name string) (string, error) {
		if p, ok := path[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { getenv, lookPath = prevEnv, prevLook })
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "usr", "bin")
	sysFF, sysFP := exe(t, sys, "ffmpeg"), exe(t, sys, "ffprobe")
	own := filepath.Join(root, "opt", "ff")
	ownFF, ownFP := exe(t, own, "ffmpeg"), exe(t, own, "ffprobe")
	app := filepath.Join(root, "payload")
	appFF, appFP := exe(t, filepath.Join(app, "ffmpeg"), "ffmpeg"), exe(t, filepath.Join(app, "ffmpeg"), "ffprobe")
	onPath := map[string]string{"ffmpeg": sysFF, "ffprobe": sysFP, "ffmpeg-7": ownFF}

	for _, c := range []struct {
		name    string
		media   config.MediaSection
		env     map[string]string
		path    map[string]string
		payload string
		want    Tools
	}{
		{"the PATH's", config.MediaSection{}, nil, onPath, "", Tools{FFmpeg: sysFF, FFprobe: sysFP, Source: FromPath}},
		{"none", config.MediaSection{}, nil, nil, "", Tools{Source: FromPath}},
		{"the app's", config.MediaSection{}, nil, onPath, app, Tools{FFmpeg: appFF, FFprobe: appFP, Source: FromApp}},
		{"a payload without ffmpeg (Linux)", config.MediaSection{}, nil, onPath, root, Tools{FFmpeg: sysFF, FFprobe: sysFP, Source: FromPath}},
		{"the setting, ffprobe beside it", config.MediaSection{FFmpeg: ownFF}, nil, onPath, app, Tools{FFmpeg: ownFF, FFprobe: ownFP, Source: FromConfig}},
		{"the setting by name", config.MediaSection{FFmpeg: "ffmpeg-7", FFprobe: sysFP}, nil, onPath, "", Tools{FFmpeg: ownFF, FFprobe: sysFP, Source: FromConfig}},
		{"the setting names nothing", config.MediaSection{FFmpeg: filepath.Join(root, "nope", "ffmpeg")}, nil, onPath, app,
			Tools{FFprobe: sysFP, Source: FromConfig, Setting: "[media] ffmpeg", Wanted: filepath.Join(root, "nope", "ffmpeg")}},
		{"the environment", config.MediaSection{}, map[string]string{"MAKTABA_FFMPEG_BIN": ownFF}, onPath, app, Tools{FFmpeg: ownFF, FFprobe: ownFP, Source: FromEnv}},
		{"the setting before the environment", config.MediaSection{FFmpeg: sysFF}, map[string]string{"MAKTABA_FFMPEG": ownFF, "MAKTABA_FFPROBE": ownFP}, onPath, "",
			Tools{FFmpeg: sysFF, FFprobe: ownFP, Source: FromConfig}},
		{"ffprobe's environment names nothing", config.MediaSection{}, map[string]string{"MAKTABA_FFPROBE": "/x/ffprobe"}, onPath, "",
			Tools{FFmpeg: sysFF, Source: FromPath, Setting: "MAKTABA_FFPROBE", Wanted: "/x/ffprobe"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeEnv(t, c.env, c.path)
			if got := Find(c.media, c.payload); got != c.want {
				t.Errorf("\n got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// Every role gets the four names, and an ffmpeg off the PATH goes first on it.
func TestEnv(t *testing.T) {
	fakeEnv(t, map[string]string{"PATH": "/usr/bin"}, nil)
	ff, fp := filepath.Join("/opt", "ff", "ffmpeg"), filepath.Join("/opt", "ff", "ffprobe")
	got := Tools{FFmpeg: ff, FFprobe: fp, Source: FromConfig}.Env()
	want := []string{"PATH=" + filepath.Dir(ff) + string(os.PathListSeparator) + "/usr/bin",
		"MAKTABA_FFMPEG=" + ff, "MAKTABA_FFMPEG_BIN=" + ff, "MAKTABA_FFPROBE=" + fp, "MAKTABA_FFPROBE_BIN=" + fp}
	if !slices.Equal(got, want) {
		t.Errorf("env %q", got)
	}
	if got := (Tools{FFmpeg: ff, FFprobe: fp, Source: FromPath}).Env(); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "PATH=") }) {
		t.Errorf("the PATH's own changes no PATH: %q", got)
	}
	// [media] ffmpeg = the PATH's own ("/usr/bin/ffmpeg"): the PATH stays as it is, so
	// /usr/local/bin's yt-dlp is still found before /usr/bin's
	sys := exe(t, filepath.Join(t.TempDir(), "usr", "bin"), "ffmpeg")
	fakeEnv(t, map[string]string{"PATH": "/usr/local/bin" + string(os.PathListSeparator) + filepath.Dir(sys)}, map[string]string{"ffmpeg": sys})
	if got := (Tools{FFmpeg: sys, Source: FromConfig}).Env(); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "PATH=") }) {
		t.Errorf("the setting names the PATH's own: %q", got)
	}
	if got := (Tools{Source: FromPath}).Env(); got != nil {
		t.Errorf("none found: %q", got)
	}
}

// The probes' encode is a TV block in small: libx264, AAC and speechnorm as the blocks set it.
func TestEncodeIsABlock(t *testing.T) {
	args := strings.Join(testEncode, " ")
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`-c:v libx264\b`), regexp.MustCompile(`-c:a aac\b`),
		regexp.MustCompile(`speechnorm=e=[0-9.]+:r=[0-9.]+:l=1`), regexp.MustCompile(`-f mpegts -$`),
	} {
		if !want.MatchString(args) {
			t.Errorf("%s: no %s", args, want)
		}
	}
}
