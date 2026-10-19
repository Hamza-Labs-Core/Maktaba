package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func writeExe(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Story 35.2 TC5: under the launcher every role is started from the current
// release's directory — never from PATH, BinDir or next to the launcher.
func TestSupervisorUsesReleaseDir(t *testing.T) {
	t.Setenv("MAKTABA_API_BIN", "")
	t.Setenv("MAKTABA_STREAMING_BIN", "")
	t.Setenv("MAKTABA_PYTHON", "")
	root := t.TempDir()
	rel := filepath.Join(root, "bin", "releases", "1.5.0")
	writeExe(t, filepath.Join(rel, exe("maktaba-api")))
	writeExe(t, filepath.Join(rel, exe("maktaba-streaming")))
	venvPy := filepath.Join(rel, "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		venvPy = filepath.Join(rel, "venv", "Scripts", "python.exe")
	}
	writeExe(t, venvPy)
	// a decoy on BinDir and on PATH
	decoy := t.TempDir()
	writeExe(t, filepath.Join(decoy, exe("maktaba-api")))
	writeExe(t, filepath.Join(decoy, exe("maktaba-streaming")))
	t.Setenv("PATH", decoy+string(os.PathListSeparator)+os.Getenv("PATH"))

	opts := Options{Config: config.Defaults(), BinDir: decoy, ReleaseDir: rel}
	for role, want := range map[Role]string{
		RoleAPI:       filepath.Join(rel, exe("maktaba-api")),
		RoleStreaming: filepath.Join(rel, exe("maktaba-streaming")),
		RolePipeline:  venvPy,
	} {
		p, err := Build(role, opts)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if p.Cmd.Path != want {
			t.Errorf("%s runs %s, want %s", role, p.Cmd.Path, want)
		}
	}
	slot, err := BuildSlot("blue", opts)
	if err != nil {
		t.Fatal(err)
	}
	if slot.Cmd.Path != filepath.Join(rel, exe("maktaba-streaming")) {
		t.Errorf("tv-slot runs %s", slot.Cmd.Path)
	}

	// a binary missing from the release is an error, not a fallback to PATH
	if err := os.Remove(filepath.Join(rel, exe("maktaba-api"))); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(RoleAPI, opts); !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("missing api in the release: %v", err)
	}
}

// The slot release root under the launcher is <install>/bin (Epic 33 §3.3 =
// Epic 35 §2.1), not the current release's directory.
func TestReleaseBinRootUnderLauncher(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join(root, "bin", "releases", "1.5.0")
	t.Setenv("MAKTABA_RELEASE_DIR", rel)
	if got := releaseBinRoot(filepath.Join(rel, exe("maktaba-streaming"))); got != filepath.Join(root, "bin") {
		t.Fatalf("releaseBinRoot = %s", got)
	}
	t.Setenv("MAKTABA_RELEASE_DIR", "")
	if got := releaseBinRoot(filepath.Join(root, "x", exe("maktaba-streaming"))); got != filepath.Join(root, "x") {
		t.Fatalf("releaseBinRoot without the launcher = %s", got)
	}
}
