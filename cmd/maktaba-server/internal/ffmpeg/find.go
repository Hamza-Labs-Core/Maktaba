// Package ffmpeg finds the ffmpeg and ffprobe every role runs, and checks that
// they do what Maktaba asks of them. The Mac and Windows apps carry their own
// (the payload's ffmpeg/); on Linux Maktaba runs the distribution's (Epic 39:
// the .deb and the .rpm depend on it), so setup's check, `maktaba-server
// ffmpeg` and the supervisor's status say what to install when it is missing,
// too old, or lacks an encoder, filter or format.
package ffmpeg

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Source is where the ffmpeg was found.
type Source string

const (
	// FromConfig: [media] ffmpeg in server.toml.
	FromConfig Source = "config"
	// FromEnv: $MAKTABA_FFMPEG (or $MAKTABA_FFMPEG_BIN) in the server's environment.
	FromEnv Source = "env"
	// FromApp: the desktop app payload's ffmpeg/ (Mac, Windows).
	FromApp Source = "app"
	// FromPath: the PATH (on Linux, the distribution's).
	FromPath Source = "path"
)

// Tools are the ffmpeg and ffprobe the roles run: full paths, "" for one not
// found.
type Tools struct {
	FFmpeg  string `json:"ffmpeg"`
	FFprobe string `json:"ffprobe"`
	// Source is where FFmpeg came from (or would have: a setting naming a
	// program that is not there).
	Source Source `json:"source"`
	// Setting and Wanted: the setting that names a program, and what it names,
	// when that program is not there (FFmpeg or FFprobe is then "").
	Setting string `json:"setting,omitempty"`
	Wanted  string `json:"wanted,omitempty"`
}

// what tests replace
var (
	lookPath = exec.LookPath
	getenv   = os.Getenv
)

// Find resolves the tools the roles run. ffmpeg: [media] ffmpeg, else
// $MAKTABA_FFMPEG / $MAKTABA_FFMPEG_BIN, else the payload's ffmpeg/ (payload:
// the desktop app's server payload this server runs from, "" outside one),
// else the PATH. ffprobe: [media] ffprobe, else $MAKTABA_FFPROBE /
// $MAKTABA_FFPROBE_BIN, else the one beside that ffmpeg, else the PATH. A
// setting naming a program that is not there is not passed over: the check
// says so.
func Find(media config.MediaSection, payload string) Tools {
	var t Tools
	switch {
	case strings.TrimSpace(media.FFmpeg) != "":
		t.Source = FromConfig
		t.FFmpeg = t.resolve("[media] ffmpeg", media.FFmpeg)
	case env("MAKTABA_FFMPEG", "MAKTABA_FFMPEG_BIN") != "":
		name, v := envNamed("MAKTABA_FFMPEG", "MAKTABA_FFMPEG_BIN")
		t.Source = FromEnv
		t.FFmpeg = t.resolve(name, v)
	case payload != "" && isExecutable(filepath.Join(payload, "ffmpeg", programFile("ffmpeg"))):
		t.Source = FromApp
		t.FFmpeg = filepath.Join(payload, "ffmpeg", programFile("ffmpeg"))
	default:
		t.Source = FromPath
		if p, err := lookPath("ffmpeg"); err == nil {
			t.FFmpeg = absolute(p)
		}
	}
	switch {
	case strings.TrimSpace(media.FFprobe) != "":
		t.FFprobe = t.resolve("[media] ffprobe", media.FFprobe)
	case env("MAKTABA_FFPROBE", "MAKTABA_FFPROBE_BIN") != "":
		t.FFprobe = t.resolve(envNamed("MAKTABA_FFPROBE", "MAKTABA_FFPROBE_BIN"))
	case t.FFmpeg != "" && isExecutable(filepath.Join(filepath.Dir(t.FFmpeg), programFile("ffprobe"))):
		t.FFprobe = filepath.Join(filepath.Dir(t.FFmpeg), programFile("ffprobe"))
	default:
		if p, err := lookPath("ffprobe"); err == nil {
			t.FFprobe = absolute(p)
		}
	}
	return t
}

// resolve is the program a setting names: a path (it must be there and run), or
// a bare name looked up on the PATH. "" when it is not there, with the setting
// and its value kept for the message (the first one only).
func (t *Tools) resolve(setting, v string) string {
	v = strings.TrimSpace(v)
	if strings.ContainsAny(v, `/\`) {
		if isExecutable(v) {
			return absolute(v)
		}
	} else if p, err := lookPath(v); err == nil {
		return absolute(p)
	}
	if t.Setting == "" {
		t.Setting, t.Wanted = setting, v
	}
	return ""
}

// Env is what the roles get so each runs these tools: MAKTABA_FFMPEG and
// MAKTABA_FFMPEG_BIN (the pipeline's and the api's name, the streaming
// role's), MAKTABA_FFPROBE and MAKTABA_FFPROBE_BIN, and, when a bare "ffmpeg"
// on the PATH is another one, its folder first on the PATH (what runs a bare
// "ffmpeg", yt-dlp too, gets it). Not when the PATH finds this one already:
// [media] ffmpeg = "/usr/bin/ffmpeg" leaves /usr/local/bin and ~/.local/bin
// (a newer yt-dlp) ahead of /usr/bin. Nothing for a tool not found: the roles
// then look for it on the PATH themselves, and the check says what is missing.
func (t Tools) Env() []string {
	var env []string
	if t.FFmpeg != "" {
		if t.Source != FromPath && !t.onPath() {
			env = append(env, "PATH="+filepath.Dir(t.FFmpeg)+string(os.PathListSeparator)+getenv("PATH"))
		}
		env = append(env, "MAKTABA_FFMPEG="+t.FFmpeg, "MAKTABA_FFMPEG_BIN="+t.FFmpeg)
	}
	if t.FFprobe != "" {
		env = append(env, "MAKTABA_FFPROBE="+t.FFprobe, "MAKTABA_FFPROBE_BIN="+t.FFprobe)
	}
	return env
}

// onPath: a bare "ffmpeg" on the PATH is t.FFmpeg (the same file, links
// resolved).
func (t Tools) onPath() bool {
	p, err := lookPath("ffmpeg")
	if err != nil {
		return false
	}
	return sameFile(p, t.FFmpeg)
}

func sameFile(a, b string) bool {
	for _, p := range []*string{&a, &b} {
		if r, err := filepath.EvalSymlinks(*p); err == nil {
			*p = r
		}
		*p = absolute(*p)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// env is the first of the variables that is set.
func env(names ...string) string {
	_, v := envNamed(names...)
	return v
}

func envNamed(names ...string) (string, string) {
	for _, n := range names {
		if v := strings.TrimSpace(getenv(n)); v != "" {
			return n, v
		}
	}
	return "", ""
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" { // no execute bits: Windows runs a program by its suffix
		return strings.EqualFold(filepath.Ext(p), ".exe")
	}
	return info.Mode()&0o111 != 0
}

func absolute(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// programFile is a program's file name on this OS (ffmpeg.exe on Windows,
// whose payload keeps them so: Epic 38 story 38.11).
func programFile(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
