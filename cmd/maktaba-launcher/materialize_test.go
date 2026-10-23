package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// Review B-H2: on Windows a kept payload is put back into the app's own folder (the
// firewall's rules name the programs by path): the kept release's files in place, the
// failed release's additions gone (a second pipeline wheel would stop the venv build),
// the running launcher left alone; then the override is cleared and the app's folder runs.
func TestKeptPayloadPutBackInPlace(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "server")
	kept := filepath.Join(t.TempDir(), "payloads", "1.4.0")
	writeTree(t, bundle, map[string]string{
		"payload.json":                              `{"schema":1,"version":"1.5.0"}`,
		"bin/maktaba-server.exe":                    "server 1.5.0",
		"bin/maktaba-launcher.exe":                  "launcher (running)",
		"pipeline/maktaba_pipeline-1.5.0-py3.whl":   "wheel 1.5.0",
		"pipeline/maktaba_pipeline-constraints.txt": "new",
	})
	writeTree(t, kept, map[string]string{
		"payload.json":                              `{"schema":1,"version":"1.4.0"}`,
		"bin/maktaba-server.exe":                    "server 1.4.0",
		"bin/maktaba-launcher.exe":                  "launcher 1.4.0",
		"pipeline/maktaba_pipeline-1.4.0-py3.whl":   "wheel 1.4.0",
		"pipeline/maktaba_pipeline-constraints.txt": "old",
		keptMarker: "1.4.0\n",
	})
	self := filepath.Join(bundle, "bin", "maktaba-launcher.exe")
	if err := materialize(bundle, kept, self); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(bundle, filepath.FromSlash(name)))
		if err != nil {
			return "<none>"
		}
		return string(b)
	}
	for name, want := range map[string]string{
		"payload.json":                              `{"schema":1,"version":"1.4.0"}`,
		"bin/maktaba-server.exe":                    "server 1.4.0",
		"bin/maktaba-launcher.exe":                  "launcher (running)",
		"pipeline/maktaba_pipeline-1.4.0-py3.whl":   "wheel 1.4.0",
		"pipeline/maktaba_pipeline-1.5.0-py3.whl":   "<none>",
		"pipeline/maktaba_pipeline-constraints.txt": "old",
		keptMarker: "<none>",
	} {
		if got := read(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// a copy cut short (no marker) is never put back
	if err := materialize(bundle, t.TempDir(), self); err == nil {
		t.Fatal("a copy without its marker was put back")
	}

	// the launcher: the override materialized and cleared, the app's folder runs
	root := t.TempDir()
	writeTree(t, bundle, map[string]string{"pipeline/maktaba_pipeline-1.5.0-py3.whl": "wheel 1.5.0"})
	if err := layout.WriteOverride(root, kept); err != nil {
		t.Fatal(err)
	}
	prev := materializeOnWindows
	materializeOnWindows = true
	defer func() { materializeOnWindows = prev }()
	ln := &launcher{cfg: Config{Root: root, Bundle: bundle}, l: layout.New(root),
		log: newEventLog(layout.New(root).LauncherLog(), io.Discard, time.Now)}
	defer ln.log.Close()
	if p := ln.bundlePayload(); p != bundle {
		t.Fatalf("runs %q, want the app's folder", p)
	}
	if o := layout.ReadOverride(root); o != "" {
		t.Fatalf("override %q left", o)
	}
	if read("pipeline/maktaba_pipeline-1.5.0-py3.whl") != "<none>" {
		t.Fatal("the failed release's wheel is left")
	}
}

// Story 35.7 AC6: the server's request to hand over to a new launcher is taken once,
// and only when it names a launcher that exists.
func TestReplaceRequestTakenOnce(t *testing.T) {
	root := t.TempDir()
	ln := &launcher{cfg: Config{Root: root}}
	updates := layout.New(root).UpdatesDir()
	_ = os.MkdirAll(updates, 0o755)
	next := filepath.Join(t.TempDir(), "maktaba-launcher")
	_ = os.WriteFile(next, []byte("launcher 2"), 0o755)
	req := filepath.Join(updates, layout.ReplaceLauncherFile)
	_ = os.WriteFile(req, []byte(next+"\n"), 0o644)
	if got := ln.takeReplaceRequest(); got != next {
		t.Fatalf("request %q, want %q", got, next)
	}
	if got := ln.takeReplaceRequest(); got != "" {
		t.Fatalf("taken twice: %q", got)
	}
	_ = os.WriteFile(req, []byte(filepath.Join(t.TempDir(), "gone")+"\n"), 0o644)
	if got := ln.takeReplaceRequest(); got != "" {
		t.Fatalf("a launcher that is not there: %q", got)
	}
	_ = os.WriteFile(req, []byte("relative/maktaba-launcher\n"), 0o644)
	if got := ln.takeReplaceRequest(); got != "" {
		t.Fatalf("a relative path: %q", got)
	}
}
