package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// -files-json writes the files.json the home's reader finds in the package, so its digest is
// the one firstparty.CheckPackage looks for in checksums.txt (story 54.27 §2).
func TestFilesJSONBesideThePackage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the command")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "i18n"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "manifest.toml"), []byte("schema = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "i18n", "en.json"), []byte("{}"), 0o644)
	out := filepath.Join(dir, "x.mkplugin")
	fj := filepath.Join(dir, "x.files.json")
	cmd := exec.Command("go", "run", ".", "-files-json", fj, src, out)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	beside, err := os.ReadFile(fj)
	if err != nil {
		t.Fatal(err)
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
	if string(beside) != string(pkg.FilesJSON) {
		t.Fatalf("beside %s, inside %s", beside, pkg.FilesJSON)
	}
	// the printed digest is files.json's
	if got := strings.TrimSpace(string(b)); got != pkg.FilesSHA256 {
		t.Fatalf("printed %q, files.json is %s", got, pkg.FilesSHA256)
	}
}

// -developer-sig puts the signature in the package's head and leaves files.json as it was
// (story 54.45 §2).
func TestADeveloperSigLeavesFilesJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the command")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "i18n"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "manifest.toml"), []byte("schema = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "i18n", "en.json"), []byte("{}"), 0o644)
	sig := filepath.Join(dir, "developer.sig")
	_ = os.WriteFile(sig, []byte(`{"key_id":"x","sig":"y","signed":{}}`), 0o644)
	read := func(args ...string) *componentpack.PluginPackage {
		out := filepath.Join(t.TempDir(), "x.mkplugin")
		cmd := exec.Command("go", append(append([]string{"run", "."}, args...), src, out)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, b)
		}
		f, _ := os.Open(out)
		defer func() { _ = f.Close() }()
		p, err := componentpack.UnpackPlugin(f, filepath.Join(t.TempDir(), "u"), componentpack.PluginExpect{})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	plain, signed := read(), read("-developer-sig", sig)
	if string(signed.DeveloperSig) != `{"key_id":"x","sig":"y","signed":{}}` || plain.DeveloperSig != nil {
		t.Fatalf("developer.sig: %q / %q", signed.DeveloperSig, plain.DeveloperSig)
	}
	if string(plain.FilesJSON) != string(signed.FilesJSON) {
		t.Fatal("the signature changed files.json")
	}
}
