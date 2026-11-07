package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

// The packed folder unpacks with the home's reader, every file listed.
func TestPackUnpacks(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the command")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "i18n"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "manifest.toml"), []byte("schema = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "i18n", "en.json"), []byte("{}"), 0o644)
	out := filepath.Join(dir, "x.mkplugin")
	cmd := exec.Command("go", "run", ".", src, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	pkg, err := componentpack.UnpackPlugin(f, filepath.Join(dir, "unpacked"), componentpack.PluginExpect{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Files) != 2 {
		t.Fatalf("files %+v", pkg.Files)
	}
}
