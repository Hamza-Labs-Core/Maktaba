// Package symbolstest unpacks the symbols package's Mach-O and ELF fixtures for tests: they
// are kept gzip'd (testdata/build.sh, testdata/build-native.sh), since the repo's lint refuses
// committed executables.
package symbolstest

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Fixture is the path of name ("crashy.dwarf", "player.dwarf", "crashy.arm64", "native.so",
// "native.stripped.so") unpacked into the test's temporary directory.
func Fixture(t testing.TB, name string) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	in, err := os.Open(filepath.Join(filepath.Dir(here), "..", "testdata", name+".gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	zr, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), name)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, zr); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}
