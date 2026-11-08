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
