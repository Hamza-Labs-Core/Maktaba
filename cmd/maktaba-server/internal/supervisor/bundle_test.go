//go:build !windows

package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// payload lays out a Maktaba.app server payload with the given executables.
func payload(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Epic 36: inside Maktaba.app every role gets the payload's ffmpeg and the
// pipeline runs from the venv built from the payload's wheel.
func TestAppBundleFFmpegAndVenv(t *testing.T) {
	for _, k := range []string{"MAKTABA_FFMPEG", "MAKTABA_FFMPEG_BIN", "MAKTABA_FFPROBE", "MAKTABA_FFPROBE_BIN", "MAKTABA_PYTHON"} {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // no ffmpeg of this machine's
	p := payload(t, "ffmpeg/ffmpeg", "ffmpeg/ffprobe")
	t.Setenv("MAKTABA_APP_BUNDLE", p)
	cfg := config.Defaults()

	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		m := envMap(childEnvOK(role, cfg))
		ff := filepath.Join(p, "ffmpeg")
		if m["MAKTABA_FFMPEG_BIN"] != filepath.Join(ff, "ffmpeg") || m["MAKTABA_FFPROBE"] != filepath.Join(ff, "ffprobe") ||
			!strings.HasPrefix(m["PATH"], ff+string(os.PathListSeparator)) {
			t.Errorf("%s: ffmpeg env = %v", role, m)
		}
	}
	// the operator's own wins over the app's, and every role gets it
	own := payload(t, "opt/ffmpeg", "opt/ffprobe")
	t.Setenv("MAKTABA_FFMPEG", filepath.Join(own, "opt", "ffmpeg"))
	if m := envMap(childEnvOK(RoleStreaming, cfg)); m["MAKTABA_FFMPEG_BIN"] != filepath.Join(own, "opt", "ffmpeg") ||
		m["MAKTABA_FFPROBE_BIN"] != filepath.Join(own, "opt", "ffprobe") {
		t.Errorf("the operator's MAKTABA_FFMPEG: %v", m)
	}
	// [media] ffmpeg wins over both
	withSetting := cfg
	withSetting.Media.FFmpeg = filepath.Join(p, "ffmpeg", "ffmpeg")
	if m := envMap(childEnvOK(RoleAPI, withSetting)); m["MAKTABA_FFMPEG"] != withSetting.Media.FFmpeg {
		t.Errorf("[media] ffmpeg: %v", m)
	}
	t.Setenv("MAKTABA_FFMPEG", "")

	// always the app's venv, also before it exists (the pipeline's Prepare builds
	// it before the first start, Epic 38 story 38.12)
	venvPy := filepath.Join(cfg.StateDir(), "venv", "bin", "python")
	if py := pythonFor(Options{Config: cfg}); py != venvPy {
		t.Errorf("python before the venv = %s, want %s", py, venvPy)
	}
	if err := os.MkdirAll(filepath.Dir(venvPy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(venvPy, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if py := pythonFor(Options{Config: cfg}); py != venvPy {
		t.Errorf("python = %s, want the app's venv %s", py, venvPy)
	}

	t.Setenv("MAKTABA_APP_BUNDLE", "")
	if m := envMap(childEnvOK(RoleAPI, cfg)); m["MAKTABA_FFMPEG_BIN"] != "" || m["PATH"] != "" {
		t.Errorf("outside the app, without an ffmpeg on the PATH, nothing changes: %v", m)
	}
	if py := pythonFor(Options{Config: cfg}); py != "python3" {
		t.Errorf("outside the app python = %s", py)
	}

	// on Linux the app carries none: the PATH's (the distribution's), the PATH as it is
	sys := payload(t, "bin/ffmpeg", "bin/ffprobe")
	t.Setenv("PATH", filepath.Join(sys, "bin"))
	t.Setenv("MAKTABA_APP_BUNDLE", payload(t, "bin/maktaba-server"))
	if m := envMap(childEnvOK(RolePipeline, cfg)); m["MAKTABA_FFMPEG"] != filepath.Join(sys, "bin", "ffmpeg") ||
		m["MAKTABA_FFPROBE"] != filepath.Join(sys, "bin", "ffprobe") || m["PATH"] != "" {
		t.Errorf("the PATH's ffmpeg: %v", m)
	}
}
