package updater

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// A worker installed without the stt extra (install-node's old default, extras none) on a
// PC whose NVIDIA GPU transcribes gets it with its next update: the release's environment
// is built with stt beside what it had, so the stt extra's CTranslate2 and CUDA wheels are
// checked in, and the worker transcribes on its GPU. Without such a GPU it keeps what it
// had.
func TestReleaseVenvAddsSTTOnAWhisperGPU(t *testing.T) {
	l := layout.New(t.TempDir())
	if err := l.WritePointer(layout.CurrentFile, "1.4.0"); err != nil {
		t.Fatal(err)
	}
	li := &LayoutInstaller{Layout: l, Version: "1.4.0"}
	py := venvPython(filepath.Join(l.ReleaseDir("1.4.0"), "venv"))
	if err := os.MkdirAll(filepath.Dir(py), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(py, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	prevProbe, prevGPU := probeExtras, whisperGPU
	t.Cleanup(func() { probeExtras, whisperGPU = prevProbe, prevGPU })
	have := []string{"search", "mediaintel"}
	probeExtras = func(context.Context, string) []string { return have }
	rtx := hostinfo.GPU{Name: "NVIDIA GeForce RTX 2080 SUPER", MemoryMB: 8192, ComputeCap: "7.5", Driver: "581.57"}
	gpu := true
	whisperGPU = func() (hostinfo.GPU, bool) { return rtx, gpu }

	if got := li.extras(context.Background()); got != "mediaintel,search,stt" {
		t.Errorf("an RTX 2080 SUPER without the stt extra: extras %q, want stt added", got)
	}
	have = []string{"stt", "search"}
	if got := li.extras(context.Background()); got != "search,stt" {
		t.Errorf("stt already: extras %q", got)
	}
	have = []string{"search"}
	gpu = false
	if got := li.extras(context.Background()); got != "search" {
		t.Errorf("no GPU Whisper runs on: extras %q, want what it had", got)
	}
}
