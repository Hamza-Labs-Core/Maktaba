package main

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// The signature verifies with the seed's key, names the plugin and version, and covers the
// files.json given (story 54.45 §2).
func TestSignsFilesJSON(t *testing.T) {
	dir := t.TempDir()
	seed := sha256.Sum256([]byte("maktaba-plugins TEST KEY, never for production: maktaba-developer"))
	key := filepath.Join(dir, "k.key")
	fj := filepath.Join(dir, "files.json")
	out := filepath.Join(dir, "developer.sig")
	_ = os.WriteFile(key, []byte(base64.StdEncoding.EncodeToString(seed[:])+"\n"), 0o600)
	files := []byte(`[{"path":"manifest.toml","sha256":"` + "00" + `","size":1}]`)
	_ = os.WriteFile(fj, files, 0o644)
	if code := run([]string{"-key", key, "-plugin", "tv.maktaba.thumbnails", "-version", "1.0.0", "-files-json", fj, "-out", out}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	sig, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	res, err := pluginsig.VerifyPackage(pluginsig.Package{FilesJSON: files, DeveloperSig: sig, PluginID: "tv.maktaba.thumbnails", Version: "1.0.0"}, nil)
	if err != nil || res.DeveloperKeyID != pluginsig.NewSigner(seed[:]).ID || res.Level != pluginsig.LevelDeveloper {
		t.Fatalf("%+v %v", res, err)
	}
	// another version than the package's: refused by the home
	if _, err := pluginsig.VerifyPackage(pluginsig.Package{FilesJSON: files, DeveloperSig: sig, PluginID: "tv.maktaba.thumbnails", Version: "1.0.1"}, nil); err == nil {
		t.Fatal("a signature for another version verified")
	}
}

func TestBadInput(t *testing.T) {
	dir := t.TempDir()
	if run(nil) != 2 {
		t.Fatal("no flags: not a usage error")
	}
	short := filepath.Join(dir, "short.key")
	_ = os.WriteFile(short, []byte(base64.StdEncoding.EncodeToString([]byte("too short"))), 0o600)
	fj := filepath.Join(dir, "files.json")
	_ = os.WriteFile(fj, []byte("[]"), 0o644)
	if run([]string{"-key", short, "-plugin", "x", "-version", "1.0.0", "-files-json", fj, "-out", filepath.Join(dir, "o")}) != 1 {
		t.Fatal("a key that is not a 32-byte seed was used")
	}
}
