package firstparty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

const relVersion = "0.9.0"

// buildRelease runs tools/plugins/build.py, the release's own build, into a fresh folder.
func buildRelease(t *testing.T, version string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds every first-party package")
	}
	out := t.TempDir()
	cmd := exec.Command("python3", filepath.Join("..", "..", "..", "..", "tools", "plugins", "build.py"), "--mkplugin", packer(t), "--version", version, "--out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build.py: %v: %s", err, b)
	}
	return out
}

// checksums is the release's "Generate checksums" step: sha256sum of every file.
func checksums(t *testing.T, dir string, skip ...string) releaseverify.Checksums {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.Name() == releaseverify.ChecksumsFile || contains(skip, e.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s  ./%s\n", sha256Hex(raw), e.Name())
	}
	if err := os.WriteFile(filepath.Join(dir, releaseverify.ChecksumsFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	sums, err := releaseverify.ParseChecksums([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return sums
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// T01: a release's packages, from build.py, checked against its checksums: one per plugin of
// the embedded bundle, every one trusted.
func TestTheReleasesPackagesAreTrusted(t *testing.T) {
	dir := buildRelease(t, relVersion)
	names, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	plugins, _ := Plugins()
	if len(names) != len(plugins) || len(names) == 0 {
		t.Fatalf("%d packages for %d plugins", len(names), len(plugins))
	}
	var want []string
	for _, fp := range plugins {
		want = append(want, PackageName(fp.ID, relVersion))
	}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("checked %v, want %v", names, want)
	}
	// build.py is reproducible: a re-run of the release signs the same checksums
	again := buildRelease(t, relVersion)
	a, b := checksums(t, dir), checksums(t, again)
	for n, d := range a {
		if n != releaseverify.ChecksumsFile && b[n] != d {
			t.Errorf("%s differs between two builds", n)
		}
	}
}

func TestAPackageMissingFromTheChecksumsIsUnsigned(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	for _, skip := range []string{FilesJSONName(fp.ID, relVersion), PackageName(fp.ID, relVersion)} {
		_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir, skip))
		if codeOf(err) != CodeTrustedUnsigned {
			t.Fatalf("%s not listed: %v", skip, err)
		}
	}
}

func TestAChangedByteIsUnsigned(t *testing.T) {
	dir := buildRelease(t, relVersion)
	sums := checksums(t, dir)
	fp := firstPlugin(t)
	p := filepath.Join(dir, FilesJSONName(fp.ID, relVersion))
	b, _ := os.ReadFile(p)
	b[len(b)/2] ^= 1
	_ = os.WriteFile(p, b, 0o644)
	if _, err := VerifyReleasePackages(dir, relVersion, sums); codeOf(err) != CodeTrustedUnsigned {
		t.Fatalf("a files.json changed after signing: %v", err)
	}
}

// Another release's packages renamed to this release's names: listed, but their manifests
// say the other version.
func TestAnotherReleasesPackageIsRefused(t *testing.T) {
	dir := buildRelease(t, "0.8.9")
	plugins, _ := Plugins()
	for _, fp := range plugins {
		for _, n := range [][2]string{{PackageName(fp.ID, "0.8.9"), PackageName(fp.ID, relVersion)}, {FilesJSONName(fp.ID, "0.8.9"), FilesJSONName(fp.ID, relVersion)}} {
			if err := os.Rename(filepath.Join(dir, n[0]), filepath.Join(dir, n[1])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir)); codeOf(err) != CodeTrustedVersion {
		t.Fatalf("the previous release's packages: %v", err)
	}
}

// A package rebuilt with a statement of its own, its files.json listed: signed, and still
// refused, because its SQL is not the SQL this server registers.
func TestAPackageWithOtherStatementsIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	repack(t, dir, fp.ID, "statements.json", func(b []byte) []byte {
		return bytes.Replace(b, []byte(`"text":"`), []byte(`"text":"/* more */ `), 1)
	})
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir))
	if err == nil || !strings.Contains(err.Error(), "statements.json is not the one") {
		t.Fatalf("other statements: %v", err)
	}
}

func TestAPackageWithAnotherManifestIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	repack(t, dir, fp.ID, "manifest.toml", func(b []byte) []byte {
		// a valid trusted manifest with more rights: its stages in the net group
		return bytes.Replace(b, []byte(`runner    = "light"`), []byte(`runner    = "net"`), 1)
	})
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir))
	if err == nil || !strings.Contains(err.Error(), "manifest is not the one") {
		t.Fatalf("a wider manifest: %v", err)
	}
}

func TestAPackageOfNoBundlePluginIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	b, _ := os.ReadFile(filepath.Join(dir, PackageName(fp.ID, relVersion)))
	_ = os.WriteFile(filepath.Join(dir, PackageName("tv.maktaba.extra", relVersion)), b, 0o644)
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir))
	if err == nil || !strings.Contains(err.Error(), "tv.maktaba.extra") {
		t.Fatalf("an extra package: %v", err)
	}
}

func TestAMissingPackageIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	_ = os.Remove(filepath.Join(dir, PackageName(fp.ID, relVersion)))
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir)); err == nil {
		t.Fatal("a release without one plugin's package was accepted")
	}
}

func firstPlugin(t *testing.T) *FirstParty {
	t.Helper()
	plugins, err := Plugins()
	if err != nil || len(plugins) == 0 {
		t.Fatalf("%v %v", plugins, err)
	}
	return plugins[0]
}

// repack rewrites one file of a package with the home's writer and its files.json beside it,
// as someone with the build tools could.
func repack(t *testing.T, dir, id, file string, change func([]byte) []byte) {
	t.Helper()
	pkgPath := filepath.Join(dir, PackageName(id, relVersion))
	f, err := os.Open(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	unpacked := filepath.Join(t.TempDir(), "p")
	pkg, err := componentpack.UnpackPlugin(f, unpacked, componentpack.PluginExpect{})
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"manifest.toml": pkg.Manifest}
	for _, pf := range pkg.Files {
		b, err := os.ReadFile(filepath.Join(unpacked, filepath.FromSlash(pf.Path)))
		if err != nil {
			t.Fatal(err)
		}
		files[pf.Path] = b
	}
	before := files[file]
	files[file] = change(files[file])
	if bytes.Equal(before, files[file]) {
		t.Fatalf("%s did not change", file)
	}
	var buf bytes.Buffer
	if _, err := componentpack.BuildPlugin(&buf, files, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkgPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := componentpack.UnpackPlugin(bytes.NewReader(buf.Bytes()), filepath.Join(t.TempDir(), "q"), componentpack.PluginExpect{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FilesJSONName(id, relVersion)), again.FilesJSON, 0o644); err != nil {
		t.Fatal(err)
	}
}

// packer builds the home's .mkplugin writer from this module's own graph (api requires
// componentpack), so the test needs no module beyond api's.
func packer(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "mkplugin")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, "github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack/cmd/mkplugin")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build mkplugin: %v: %s", err, b)
	}
	return exe
}
