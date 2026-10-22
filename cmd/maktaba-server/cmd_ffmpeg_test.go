package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ffmpeg"
)

// fedoraFree is what the check says of Fedora's ffmpeg-free.
func fedoraFree(_ context.Context, t ffmpeg.Tools) ffmpeg.Report {
	return ffmpeg.Report{Tools: t, Version: "6.1.2",
		Problem: &ffmpeg.Note{Code: "ffmpeg_lacks", Message: "/usr/bin/ffmpeg (FFmpeg 6.1.2) lacks what Maktaba uses: encoder libx264.",
			Params: map[string]any{"path": "/usr/bin/ffmpeg", "have": "6.1.2", "missing": "encoder libx264"}},
		Install: &ffmpeg.Note{Code: "ffmpeg_install", Message: "On Fedora Linux 40, this installs the ffmpeg Maktaba needs: sudo dnf ...",
			Params: map[string]any{"distro": "Fedora Linux 40", "command": "sudo dnf install --allowerasing ffmpeg"}},
		Warnings: []ffmpeg.Note{{Code: "ffmpeg_no_chromaprint", Message: "no chromaprint", Params: map[string]any{"path": "/usr/bin/ffmpeg"}}},
	}
}

func fakeFFmpegCheck(t *testing.T, check func(context.Context, ffmpeg.Tools) ffmpeg.Report) {
	t.Helper()
	prevCheck, prevIn := ffmpegCheck, ffmpegInCheck
	ffmpegCheck, ffmpegInCheck = check, true
	t.Cleanup(func() { ffmpegCheck, ffmpegInCheck = prevCheck, prevIn })
}

// Epic 39: on Linux check says what is wrong with the distribution's ffmpeg and
// the command that installs the one Maktaba needs (problems: Next waits), and what
// a feature beside the library lacks (a warning).
func TestSetupCheckFFmpeg(t *testing.T) {
	newSetupEnv(t)
	fakeFFmpegCheck(t, fedoraFree)
	_, _, ev := runStep(t, "check", map[string]any{})
	d := data(t, ev)
	if d["ok"] != false {
		t.Fatalf("check = %v", d)
	}
	notes := func(kind string) map[string]map[string]any {
		out := map[string]map[string]any{}
		list, _ := d[kind].([]any)
		for _, n := range list {
			n := n.(map[string]any)
			params, _ := n["params"].(map[string]any)
			out[n["code"].(string)] = params
		}
		return out
	}
	if p := notes("problems"); p["ffmpeg_lacks"]["missing"] != "encoder libx264" ||
		p["ffmpeg_install"]["command"] != "sudo dnf install --allowerasing ffmpeg" {
		t.Errorf("problems: %v", d["problems"])
	}
	if w := notes("warnings"); w["ffmpeg_no_chromaprint"] == nil {
		t.Errorf("warnings: %v", d["warnings"])
	}

	// one that works adds nothing
	fakeFFmpegCheck(t, func(_ context.Context, t ffmpeg.Tools) ffmpeg.Report {
		return ffmpeg.Report{Tools: t, Version: "4.4.2-0ubuntu0.22.04.1", OK: true}
	})
	_, _, ev = runStep(t, "check", map[string]any{})
	d = data(t, ev)
	for _, kind := range []string{"problems", "warnings"} {
		for code := range notes(kind) {
			if strings.HasPrefix(code, "ffmpeg") || strings.HasPrefix(code, "ffprobe") {
				t.Errorf("%s has %s", kind, code)
			}
		}
	}
}

// noteCode finds the codes the ffmpeg package's notes have: note("code", ...).
var noteCode = regexp.MustCompile(`\bnote\("([a-z_]+)"`)

// Every note the ffmpeg check makes has its words in both of the web's languages
// (setup.check.note.<code>): the setup screens, the terminal's setup and the System
// health page show them.
func TestFFmpegNotesHaveWords(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("internal", "ffmpeg", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range noteCode.FindAllStringSubmatch(string(src), -1) {
			codes[m[1]] = true
		}
	}
	if len(codes) < 10 {
		t.Fatalf("found %d codes: the pattern no longer finds them (%v)", len(codes), codes)
	}
	for _, lang := range []string{"en", "ar"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "i18n", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var words map[string]string
		if err := json.Unmarshal(raw, &words); err != nil {
			t.Fatal(err)
		}
		for code := range codes {
			if strings.TrimSpace(words["setup.check.note."+code]) == "" {
				t.Errorf("%s.json has no setup.check.note.%s", lang, code)
			}
		}
	}
}

// `maktaba-server ffmpeg`: the verdict first, the command, then what was found;
// exit 1 when it does not do what Maktaba needs.
func TestFFmpegCommand(t *testing.T) {
	t.Setenv("MAKTABA_CONFIG", filepath.Join(t.TempDir(), "server.toml"))
	fakeFFmpegCheck(t, fedoraFree)
	var out bytes.Buffer
	cmd := newFFmpegCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != errFFmpeg {
		t.Fatalf("err = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "problem ffmpeg_lacks ") ||
		lines[1] != "install sudo dnf install --allowerasing ffmpeg" ||
		!strings.Contains(out.String(), "\nwarning ffmpeg_no_chromaprint ") {
		t.Errorf("output:\n%s", out.String())
	}

	fakeFFmpegCheck(t, func(_ context.Context, _ ffmpeg.Tools) ffmpeg.Report {
		return ffmpeg.Report{Tools: ffmpeg.Tools{FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", Source: ffmpeg.FromPath},
			Version: "5.1.6-0+deb12u1", OK: true, HWEncoders: []string{"h264_vaapi"}}
	})
	out.Reset()
	cmd = newFFmpegCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"ok /usr/bin/ffmpeg (FFmpeg 5.1.6-0+deb12u1) does what Maktaba needs", "\nffmpeg /usr/bin/ffmpeg\n",
		"\nffprobe /usr/bin/ffprobe\n", "\nsource path\n", "\ngpu h264_vaapi\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}

	out.Reset()
	cmd = newFFmpegCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("err = %v", err)
	}
	var r ffmpeg.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || !r.OK || r.FFmpeg != "/usr/bin/ffmpeg" {
		t.Errorf("json %v: %s", err, out.String())
	}
}
