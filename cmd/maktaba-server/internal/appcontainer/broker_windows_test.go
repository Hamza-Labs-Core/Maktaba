//go:build windows

package appcontainer

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Story 54.23 on Windows with a real SMB share (CI's plugins-sandbox job on windows-2022
// makes two with New-SmbShare and names them here; never a developer's or the owner's
// machine): a runner group whose library is on a share, started through the launcher with
// the real grants, AppContainer and broker. From inside (testdata/broker_probe.py): without
// the broker it reaches no share (its jobs went to the worker); with it, it reads the
// library's files in any case, lists and stats them, and writes a subtitle (made, renamed,
// read back), at about the share's own speed (the handle goes straight to the redirector);
// its read handle writes nothing, it writes no other name and removes no video, and reaches nothing by a traversal, a
// junction in the share leading out, an 8.3 alias, a stream, or the other share; a tool
// handed a share path is refused (its job runs in the worker), and a process of the same
// AppContainer that is not the runner is not served.
func TestARunnersLibraryOnAShareThroughItsBroker(t *testing.T) {
	share, shareDir, other, otherDir := os.Getenv("MAKTABA_BROKER_SHARE"), os.Getenv("MAKTABA_BROKER_SHARE_DIR"),
		os.Getenv("MAKTABA_BROKER_OTHER_SHARE"), os.Getenv("MAKTABA_BROKER_OTHER_DIR")
	python := os.Getenv("MAKTABA_BROKER_PYTHON")
	if share == "" || shareDir == "" || other == "" || otherDir == "" || python == "" {
		if os.Getenv("MAKTABA_BROKER_REQUIRE") == "1" {
			t.Fatal("MAKTABA_BROKER_* not set: CI must run this test")
		}
		t.Skip("no SMB share made for this test (CI's windows-2022 job makes them)")
	}
	// the library on the share: a 256 MiB video, a junction leading out of the share root
	lib := filepath.Join(shareDir, "lib")
	video := filepath.Join(lib, "show", "ep1.mkv")
	if err := os.MkdirAll(filepath.Dir(video), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(video)
	if err != nil {
		t.Fatal(err)
	}
	block := make([]byte, 1<<20)
	for i := range block {
		block[i] = byte(i)
	}
	for i := 0; i < 256; i++ {
		if _, err := f.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(shareDir, "outside.txt"), []byte("not the library"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(lib, "escape"), outside).CombinedOutput(); err != nil { //nolint:gosec // the test's folders
		t.Fatalf("mklink: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "secret.txt"), []byte("another share"), 0o600); err != nil {
		t.Fatal(err)
	}

	// the control, outside the sandbox: the same file read straight from the share
	start := time.Now()
	direct, err := os.Open(share + `\lib\show\ep1.mkv`)
	if err != nil {
		t.Fatalf("the share itself: %v", err)
	}
	n, _ := io.CopyBuffer(io.Discard, direct, make([]byte, 8<<20))
	_ = direct.Close()
	controlMBs := float64(n) / (1 << 20) / time.Since(start).Seconds()

	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	group := "ci-broker"
	profile := RunnerProfile(group)
	t.Cleanup(func() {
		if name, err := windows.UTF16PtrFromString(profile); err == nil {
			_, _, _ = userenv.NewProc("DeleteAppContainerProfile").Call(uintptr(unsafe.Pointer(name)))
		}
	})
	pyDirs, err := exec.Command(python, "-c", "import sys,json; print(json.dumps(sorted({sys.prefix, sys.base_prefix, sys.exec_prefix})))").Output() //nolint:gosec // CI's python
	if err != nil {
		t.Fatal(err)
	}
	var pyRead []string
	if err := json.Unmarshal(pyDirs, &pyRead); err != nil {
		t.Fatal(err)
	}
	here, _ := filepath.Abs("testdata")
	winfiles, _ := filepath.Abs(filepath.Join("..", "..", "..", "..", "pipeline", "src", "maktaba_plugin_runner", "winfiles.py"))
	run := filepath.Join(dir, "run")
	libUNC := share + `\lib`
	pol := RunnerPolicy{
		Python:     pyRead,
		Read:       []string{here, filepath.Dir(winfiles), libUNC},
		WriteNamed: []string{libUNC},
		Processes:  4,
	}
	raw, _ := json.Marshal(pol)
	ap := askPolicy
	t.Cleanup(func() { askPolicy = ap })
	askPolicy = func(string, []string, io.Writer) ([]byte, int, error) { return raw, 0, nil }

	result := filepath.Join(run, "result.json")
	var errOut strings.Builder
	code := LaunchRunner([]string{"-group", group, "-run-dir", run, "--", python,
		filepath.Join(here, "broker_probe.py"), result, winfiles, libUNC, other}, &errOut)
	t.Logf("launcher: %s", errOut.String())
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("the probe wrote nothing: %v", err)
	}
	t.Logf("probe: %s", b)
	var found map[string]json.RawMessage
	if err := json.Unmarshal(b, &found); err != nil {
		t.Fatal(err)
	}
	outcome := func(name string) string {
		var v []json.RawMessage
		if err := json.Unmarshal(found[name], &v); err != nil || len(v) == 0 {
			return "absent"
		}
		var s string
		_ = json.Unmarshal(v[0], &s)
		return s
	}
	if !strings.Contains(errOut.String(), "plugin_runner_area_brokered") {
		t.Error("the share was not brokered")
	}
	for name, want := range map[string]string{
		"direct_read":      "refused",
		"read":             "ok",
		"exists":           "ok",
		"list":             "ok",
		"read_any_case":    "ok",
		"write_subtitle":   "ok",
		"write_other_name": "refused",
		"write_video":      "refused",
		"remove_video":     "refused",
		"traversal":        "refused",
		"junction_escape":  "refused",
		"short_name":       "refused",
		"other_share":      "refused",
		"stream":           "refused",
		"tool_on_share":    "refused",
		"child_on_pipe":    "ok",
	} {
		if got := outcome(name); got != want {
			t.Errorf("%s: %s, want %s (%s)", name, got, want, found[name])
		}
	}
	if got := outcome("write_through_read_handle"); got == "ok" || got == "absent" {
		t.Errorf("a brokered read handle wrote: %s", found["write_through_read_handle"])
	}
	if !strings.Contains(string(found["child_on_pipe"]), "closed") {
		t.Errorf("a process that is not the runner was served: %s", found["child_on_pipe"])
	}
	if s, err := os.ReadFile(filepath.Join(lib, "show", "ep1.en.srt")); err != nil || !strings.Contains(string(s), "hello") {
		t.Errorf("the subtitle on the share: %q %v", s, err)
	}
	for _, p := range []string{filepath.Join(lib, "show", "notes.txt")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written", p)
		}
	}
	if info, err := os.Stat(video); err != nil || info.Size() != 256<<20 {
		t.Errorf("the video changed: %v", err)
	}
	var read struct {
		MBs float64 `json:"mb_per_s"`
	}
	var v []json.RawMessage
	if json.Unmarshal(found["read"], &v) == nil && len(v) == 2 {
		_ = json.Unmarshal(v[1], &read)
	}
	t.Logf("broker_throughput: 256 MiB from %s: %.0f MB/s inside the AppContainer through the broker's handle, %.0f MB/s outside (the share itself)",
		share, read.MBs, controlMBs)
}

// moovAtEnd answers whether an mp4's index (its moov box) comes after its media (mdat), as
// a muxer leaves it without +faststart: a reader must seek to the end before any frame.
func moovAtEnd(t *testing.T, path string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	moov, mdat := -1, -1
	for off := 0; off+8 <= len(b); {
		size := int(uint32(b[off])<<24 | uint32(b[off+1])<<16 | uint32(b[off+2])<<8 | uint32(b[off+3]))
		switch string(b[off+4 : off+8]) {
		case "moov":
			moov = off
		case "mdat":
			mdat = off
		}
		if size < 8 {
			break
		}
		off += size
	}
	return moov > mdat && mdat >= 0
}

// timeTool runs a tool outside the sandbox (the worker's way: the share path itself) and
// answers how long it took.
func timeTool(t *testing.T, argv ...string) float64 {
	t.Helper()
	start := time.Now()
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil { //nolint:gosec // CI's ffmpeg on the test's files
		t.Fatalf("%v: %v %s", argv, err, out)
	}
	return time.Since(start).Seconds()
}

// Story 54.23 on Windows with a real SMB share: a runner group's tools on a file of its
// library on the share. From inside the group's AppContainer (testdata/broker_tools_probe.py)
// ffmpeg makes a thumbnail (a seek before the input, and thumb_small's shape through asyncio)
// and decodes the transcription input, and ffprobe reads the duration, of an mp4 whose index
// is at its end: each tool, a child in the same AppContainer and Job Object, reads the one
// file through the handle the broker opened for it, handed to it as its standard input
// (fd:). Refused before anything starts: a second share file, the library folder, the share
// root, another share, a share path as an output, a program that is not ffmpeg or ffprobe.
// A tool reaching for the share by a path the runner never saw gets nothing, and it writes
// nowhere but its granted folders. The times are compared with the same tools outside the
// sandbox on the share path (the worker's way).
func TestARunnersToolsReadAShareFileThroughItsBroker(t *testing.T) {
	share, shareDir, other, otherDir := os.Getenv("MAKTABA_BROKER_SHARE"), os.Getenv("MAKTABA_BROKER_SHARE_DIR"),
		os.Getenv("MAKTABA_BROKER_OTHER_SHARE"), os.Getenv("MAKTABA_BROKER_OTHER_DIR")
	python, ffmpeg, ffprobe := os.Getenv("MAKTABA_BROKER_PYTHON"), os.Getenv("MAKTABA_BROKER_FFMPEG"), os.Getenv("MAKTABA_BROKER_FFPROBE")
	if share == "" || shareDir == "" || other == "" || otherDir == "" || python == "" || ffmpeg == "" || ffprobe == "" {
		if os.Getenv("MAKTABA_BROKER_REQUIRE") == "1" {
			t.Fatal("MAKTABA_BROKER_* not set: CI must run this test")
		}
		t.Skip("no SMB share or ffmpeg for this test (CI's windows-2022 job makes them)")
	}
	lib := filepath.Join(shareDir, "tlib")
	video := filepath.Join(lib, "show", "ep2.mp4")
	if err := os.MkdirAll(filepath.Dir(video), 0o700); err != nil {
		t.Fatal(err)
	}
	// 20 s of 640x360 video and a tone, muxed without +faststart: the index at the end
	if out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=duration=20:size=640x360:rate=25", //nolint:gosec // CI's ffmpeg
		"-f", "lavfi", "-i", "sine=frequency=440:duration=20", "-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac",
		"-shortest", video).CombinedOutput(); err != nil {
		t.Fatalf("the test video: %v %s", err, out)
	}
	if !moovAtEnd(t, video) {
		t.Fatal("the test video's index is not at its end")
	}
	for _, p := range []string{filepath.Join(lib, "show", "ep3.mp4"), filepath.Join(otherDir, "secret.mp4")} {
		b, _ := os.ReadFile(video)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	libUNC, videoUNC := share+`\tlib`, share+`\tlib\show\ep2.mp4`

	// the worker's way, outside the sandbox: the same tools on the share path (warmed once)
	thumbArgs := func(in, out string) []string {
		return []string{ffmpeg, "-hide_banner", "-nostdin", "-threads", "1", "-ss", "10", "-i", in,
			"-frames:v", "1", "-vf", "scale=320:-2", "-q:v", "2", "-y", out}
	}
	probeArgs := []string{ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", videoUNC}
	scratch := t.TempDir()
	timeTool(t, thumbArgs(videoUNC, filepath.Join(scratch, "w.jpg"))...)
	workerThumb := timeTool(t, thumbArgs(videoUNC, filepath.Join(scratch, "w.jpg"))...)
	workerProbe := timeTool(t, probeArgs...)

	dir := mustCanonical(t, t.TempDir())
	t.Setenv("MAKTABA_DATA_DIR", dir)
	group := "ci-broker-tools"
	profile := RunnerProfile(group)
	t.Cleanup(func() {
		if name, err := windows.UTF16PtrFromString(profile); err == nil {
			_, _, _ = userenv.NewProc("DeleteAppContainerProfile").Call(uintptr(unsafe.Pointer(name)))
		}
	})
	pyDirs, err := exec.Command(python, "-c", "import sys,json; print(json.dumps(sorted({sys.prefix, sys.base_prefix, sys.exec_prefix})))").Output() //nolint:gosec // CI's python
	if err != nil {
		t.Fatal(err)
	}
	var pyRead []string
	if err := json.Unmarshal(pyDirs, &pyRead); err != nil {
		t.Fatal(err)
	}
	here, _ := filepath.Abs("testdata")
	winfiles, _ := filepath.Abs(filepath.Join("..", "..", "..", "..", "pipeline", "src", "maktaba_plugin_runner", "winfiles.py"))
	run := filepath.Join(dir, "run")
	outside := mustCanonical(t, t.TempDir())
	pol := RunnerPolicy{
		Python:     pyRead,
		Read:       []string{here, filepath.Dir(winfiles), filepath.Dir(ffmpeg), filepath.Dir(ffprobe), libUNC},
		WriteNamed: []string{libUNC},
		Processes:  4,
	}
	raw, _ := json.Marshal(pol)
	ap := askPolicy
	t.Cleanup(func() { askPolicy = ap })
	askPolicy = func(string, []string, io.Writer) ([]byte, int, error) { return raw, 0, nil }

	result := filepath.Join(run, "tools.json")
	var errOut strings.Builder
	code := LaunchRunner([]string{"-group", group, "-run-dir", run, "--", python,
		filepath.Join(here, "broker_tools_probe.py"), result, winfiles, libUNC, share, other, ffmpeg, ffprobe, run, outside}, &errOut)
	t.Logf("launcher: %s", errOut.String())
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("the probe wrote nothing: %v", err)
	}
	t.Logf("probe: %s", b)
	var found map[string][]json.RawMessage
	if err := json.Unmarshal(b, &found); err != nil {
		t.Fatal(err)
	}
	type ran struct {
		Code     int     `json:"code"`
		Seconds  float64 `json:"seconds"`
		Bytes    int     `json:"stdout_bytes"`
		JPEG     int     `json:"jpeg_bytes"`
		IsJPEG   bool    `json:"jpeg"`
		Duration float64 `json:"duration"`
		Made     bool    `json:"made"`
		Stderr   string  `json:"stderr"`
	}
	outcome := func(name string) (string, ran) {
		var s string
		var r ran
		if v := found[name]; len(v) == 2 {
			_ = json.Unmarshal(v[0], &s)
			_ = json.Unmarshal(v[1], &r)
		}
		return s, r
	}
	if s, r := outcome("thumbnail"); s != "ok" || r.Code != 0 || r.JPEG == 0 {
		t.Errorf("the thumbnail from the share: %s %+v", s, r)
	}
	if s, r := outcome("thumbnail_pipe"); s != "ok" || r.Code != 0 || !r.IsJPEG {
		t.Errorf("thumb_small's way: %s %+v", s, r)
	}
	if s, r := outcome("probe"); s != "ok" || r.Code != 0 || r.Duration < 19 || r.Duration > 21 {
		t.Errorf("the probe of an mp4 indexed at its end: %s %+v", s, r)
	}
	if s, r := outcome("audio"); s != "ok" || r.Code != 0 || r.Bytes < 19*16000*2 {
		t.Errorf("the transcription input: %s %+v", s, r)
	}
	for _, name := range []string{"second_file", "library_folder", "share_root", "other_share", "output_on_share", "not_a_handle_tool"} {
		if s, r := outcome(name); s != "refused" {
			t.Errorf("%s: %s %+v, want refused", name, s, r)
		}
	}
	if s, r := outcome("tool_reaches_share"); s != "ok" || r.Code == 0 {
		t.Errorf("a tool reached the share by itself: %s %+v", s, r)
	}
	if s, r := outcome("write_outside"); s != "ok" || r.Code == 0 {
		t.Errorf("a tool wrote outside its folders: %s %+v", s, r)
	}
	if _, err := os.Stat(filepath.Join(outside, "stray.jpg")); err == nil {
		t.Error("a tool wrote outside its folders")
	}
	if _, err := os.Stat(filepath.Join(lib, "show", "x.jpg")); err == nil {
		t.Error("a tool wrote on the share")
	}
	_, thumb := outcome("thumbnail")
	_, probe := outcome("probe")
	t.Logf("pipe:0 control (the same handle, never seeked): %s", found["pipe_control"])
	t.Logf("tool_timing: thumbnail %.3fs inside the AppContainer through the broker's handle, %.3fs in the worker's way (the share path); probe %.3fs inside, %.3fs in the worker's way",
		thumb.Seconds, workerThumb, probe.Seconds, workerProbe)
}
