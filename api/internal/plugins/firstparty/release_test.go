package firstparty

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
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
	names, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), "")
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
		_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir, skip), "")
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
	if _, err := VerifyReleasePackages(dir, relVersion, sums, ""); codeOf(err) != CodeTrustedUnsigned {
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
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); codeOf(err) != CodeTrustedVersion {
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
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), "")
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
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), "")
	if err == nil || !strings.Contains(err.Error(), "manifest is not the one") {
		t.Fatalf("a wider manifest: %v", err)
	}
}

func TestAPackageOfNoBundlePluginIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	b, _ := os.ReadFile(filepath.Join(dir, PackageName(fp.ID, relVersion)))
	_ = os.WriteFile(filepath.Join(dir, PackageName("tv.maktaba.extra", relVersion)), b, 0o644)
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), "")
	if err == nil || !strings.Contains(err.Error(), "tv.maktaba.extra") {
		t.Fatalf("an extra package: %v", err)
	}
}

func TestAMissingPackageIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	_ = os.Remove(filepath.Join(dir, PackageName(fp.ID, relVersion)))
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); err == nil {
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
	repackFiles(t, dir, id, func(files map[string][]byte) {
		before := files[file]
		files[file] = change(files[file])
		if bytes.Equal(before, files[file]) {
			t.Fatalf("%s did not change", file)
		}
	})
}

// repackFiles rewrites a package's files with the home's writer, and its files.json beside it.
func repackFiles(t *testing.T, dir, id string, change func(map[string][]byte)) {
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
	change(files)
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

// wheelBytes is a wheel of files with its RECORD, as a build would write it.
func wheelBytes(t *testing.T, info string, files map[string][]byte) []byte {
	t.Helper()
	var rec bytes.Buffer
	w := csv.NewWriter(&rec)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := sha256.Sum256(files[n])
		_ = w.Write([]string{n, "sha256=" + base64.RawURLEncoding.EncodeToString(s[:]), fmt.Sprint(len(files[n]))})
	}
	_ = w.Write([]string{info + "/RECORD", "", ""})
	w.Flush()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, n := range append(names, info+"/RECORD") {
		f, _ := z.Create(n)
		if n == info+"/RECORD" {
			_, _ = f.Write(rec.Bytes())
		} else {
			_, _ = f.Write(files[n])
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// wheelFiles reads a wheel's members.
func wheelFiles(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range z.File {
		rc, _ := f.Open()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(rc)
		_ = rc.Close()
		out[f.Name] = buf.Bytes()
	}
	return out
}

// packageWheel answers a built package's wheel: its name and members.
func packageWheel(t *testing.T, dir, id string) (string, map[string][]byte) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, PackageName(id, relVersion)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := filepath.Join(t.TempDir(), "p")
	pkg, err := componentpack.UnpackPlugin(f, out, componentpack.PluginExpect{})
	if err != nil {
		t.Fatal(err)
	}
	for _, pf := range pkg.Files {
		if strings.HasPrefix(pf.Path, "wheels/") {
			b, _ := os.ReadFile(filepath.Join(out, filepath.FromSlash(pf.Path)))
			return pf.Path, wheelFiles(t, b)
		}
	}
	t.Fatal("no wheel")
	return "", nil
}

// pipelineWheel writes a pipeline wheel into dir carrying every plugin's package files as
// the packages' wheels hold them (the pipeline wheel's build copies the same src/ folders),
// with change applied to its file list first.
func pipelineWheel(t *testing.T, dir string, change func(map[string][]byte)) string {
	t.Helper()
	files := map[string][]byte{"maktaba_pipeline/__init__.py": []byte("")}
	plugins, _ := Plugins()
	for _, fp := range plugins {
		_, members := packageWheel(t, dir, fp.ID)
		for n, b := range members {
			if !strings.Contains(n, ".dist-info/") {
				files[n] = b
			}
		}
	}
	if change != nil {
		change(files)
	}
	name := "maktaba_pipeline-" + relVersion + "-py3-none-any.whl"
	if err := os.WriteFile(filepath.Join(dir, name), wheelBytes(t, "maktaba_pipeline-"+relVersion+".dist-info", files), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

// Review of #1440: the package's code is the code of the release's signed pipeline wheel.
func TestAPackagesWheelIsThePipelineWheelsCode(t *testing.T) {
	dir := buildRelease(t, relVersion)
	name := pipelineWheel(t, dir, nil)
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), name); err != nil {
		t.Fatalf("the packages of the pipeline wheel's code: %v", err)
	}
	fp := firstPlugin(t)
	dist := "maktaba_fp_" + strings.ReplaceAll(fp.Folder, "-", "_")
	// the pipeline wheel's code differs from a package's (one file changed, or one more)
	for what, change := range map[string]func(map[string][]byte){
		"a changed file": func(f map[string][]byte) { f[dist+"/__init__.py"] = append(f[dist+"/__init__.py"], '#') },
		"a file more":    func(f map[string][]byte) { f[dist+"/extra.py"] = []byte("x = 1\n") },
	} {
		name := pipelineWheel(t, dir, change)
		if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), name); err == nil {
			t.Fatalf("%s in the pipeline wheel: accepted", what)
		}
	}
	// the pipeline wheel must itself be the release's signed file
	name = pipelineWheel(t, dir, nil)
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir, name), name); codeOf(err) != CodeTrustedUnsigned {
		t.Fatalf("an unsigned pipeline wheel: %v", err)
	}
}

// A package repacked with other code in its wheel (RECORD rewritten to match, files.json and
// checksums regenerated): refused against the pipeline wheel; a wheel whose RECORD does not
// hold is refused even without it.
func TestAPackageWithOtherCodeIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	name := pipelineWheel(t, dir, nil)
	fp := firstPlugin(t)
	dist := "maktaba_fp_" + strings.ReplaceAll(fp.Folder, "-", "_")
	wheel, members := packageWheel(t, dir, fp.ID)
	info := strings.TrimSuffix(strings.TrimPrefix(wheel, "wheels/"), "-py3-none-any.whl") + ".dist-info"
	evil := map[string][]byte{}
	for n, b := range members {
		if n != info+"/RECORD" {
			evil[n] = b
		}
	}
	evil[dist+"/__init__.py"] = []byte("import os\n")
	repack(t, dir, fp.ID, wheel, func([]byte) []byte { return wheelBytes(t, info, evil) })
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), name)
	if err == nil || !strings.Contains(err.Error(), "pipeline wheel") {
		t.Fatalf("other code, its RECORD rewritten: %v", err)
	}
	// a RECORD that does not match the wheel's files
	repack(t, dir, fp.ID, wheel, func(b []byte) []byte {
		f := wheelFiles(t, b)
		f[dist+"/__init__.py"] = append(f[dist+"/__init__.py"], '#')
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for n, c := range f {
			w, _ := z.Create(n)
			_, _ = w.Write(c)
		}
		_ = z.Close()
		return buf.Bytes()
	})
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); err == nil || !strings.Contains(err.Error(), "RECORD") {
		t.Fatalf("a wheel that does not match its RECORD: %v", err)
	}
}

// A package with a file more than its manifest, strings, statements and wheel: refused.
func TestAPackageWithAnExtraMemberIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	repackAdd(t, dir, fp.ID, "run.sh", []byte("#!/bin/sh\n"))
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); err == nil || !strings.Contains(err.Error(), "holds") {
		t.Fatalf("a package with a file more: %v", err)
	}
}

// Review of #1440: a signed files.json of no bundle plugin, on disk or only in the checksums,
// fails the release (it would trust a package this server never embedded).
func TestAnOrphanFilesJSONIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	b, _ := os.ReadFile(filepath.Join(dir, FilesJSONName(fp.ID, relVersion)))
	orphan := FilesJSONName("tv.maktaba.extra", relVersion)
	_ = os.WriteFile(filepath.Join(dir, orphan), b, 0o644)
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); codeOf(err) != CodeTrustedUnsigned || !strings.Contains(err.Error(), "tv.maktaba.extra") {
		t.Fatalf("an orphan files.json on disk: %v", err)
	}
	// listed only in the checksums
	sums := checksums(t, dir)
	_ = os.Remove(filepath.Join(dir, orphan))
	if _, err := VerifyReleasePackages(dir, relVersion, sums, ""); codeOf(err) != CodeTrustedUnsigned {
		t.Fatalf("an orphan files.json in the checksums only: %v", err)
	}
}

// repackAdd adds one file to a package (files.json regenerated beside it).
func repackAdd(t *testing.T, dir, id, path string, b []byte) {
	t.Helper()
	repackFiles(t, dir, id, func(files map[string][]byte) { files[path] = b })
}

// Second review of #1440: a pre-release builds no package and the check passes with none,
// but refuses one that is there.
func TestAPreReleaseCarriesNoPackage(t *testing.T) {
	dir := buildRelease(t, "1.2.3-rc.1")
	names, err := VerifyReleasePackages(dir, "1.2.3-rc.1", checksums(t, dir), "")
	if err != nil || len(names) != 0 {
		t.Fatalf("a pre-release: %v %v", names, err)
	}
	other := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	b, _ := os.ReadFile(filepath.Join(other, PackageName(fp.ID, relVersion)))
	_ = os.WriteFile(filepath.Join(dir, PackageName(fp.ID, "1.2.3-rc.1")), b, 0o644)
	if _, err := VerifyReleasePackages(dir, "1.2.3-rc.1", checksums(t, dir), ""); codeOf(err) != CodeTrustedUnsigned {
		t.Fatalf("a package in a pre-release: %v", err)
	}
}

// Second review of #1440: the wheel's dist-info is exactly its METADATA, WHEEL and RECORD,
// its METADATA requires only this release's pipeline, its RECORD's sizes hold, and its name
// is this version's.
func TestAWheelWithOtherMetadataIsRefused(t *testing.T) {
	fp := firstPlugin(t)
	dist := "maktaba_fp_" + strings.ReplaceAll(fp.Folder, "-", "_")
	info := dist + "-" + relVersion + ".dist-info"
	for what, change := range map[string]func(map[string][]byte){
		"another dependency": func(f map[string][]byte) {
			f[info+"/METADATA"] = append(f[info+"/METADATA"], []byte("Requires-Dist: attacker-package\n")...)
		},
		"another pipeline": func(f map[string][]byte) {
			f[info+"/METADATA"] = bytes.Replace(f[info+"/METADATA"], []byte("maktaba-pipeline=="+relVersion), []byte("maktaba-pipeline>=0"), 1)
		},
		"another version": func(f map[string][]byte) {
			f[info+"/METADATA"] = bytes.Replace(f[info+"/METADATA"], []byte("Version: "+relVersion), []byte("Version: 9.9.9"), 1)
		},
		"a dist-info file more": func(f map[string][]byte) { f[info+"/entry_points.txt"] = []byte("[console_scripts]\nx = os:system\n") },
		"a platform wheel": func(f map[string][]byte) {
			f[info+"/WHEEL"] = bytes.Replace(f[info+"/WHEEL"], []byte("py3-none-any"), []byte("cp312-cp312-linux_x86_64"), 1)
		},
	} {
		dir := buildRelease(t, relVersion)
		wheel, members := packageWheel(t, dir, fp.ID)
		files := map[string][]byte{}
		for n, b := range members {
			if n != info+"/RECORD" {
				files[n] = b
			}
		}
		change(files)
		repack(t, dir, fp.ID, wheel, func([]byte) []byte { return wheelBytes(t, info, files) })
		if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); err == nil {
			t.Fatalf("%s: accepted", what)
		}
	}
	// a wheel under another version's name
	dir := buildRelease(t, relVersion)
	wheel, _ := packageWheel(t, dir, fp.ID)
	repackFiles(t, dir, fp.ID, func(f map[string][]byte) {
		f["wheels/"+dist+"-9.9.9-py3-none-any.whl"] = f[wheel]
		delete(f, wheel)
	})
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); err == nil {
		t.Fatal("a wheel named for another version: accepted")
	}
}

func TestARecordWithAWrongSizeIsRefused(t *testing.T) {
	fp := firstPlugin(t)
	dist := "maktaba_fp_" + strings.ReplaceAll(fp.Folder, "-", "_")
	for _, size := range []string{"0", "not-a-number", "-1"} {
		dir := buildRelease(t, relVersion)
		wheel, _ := packageWheel(t, dir, fp.ID)
		repack(t, dir, fp.ID, wheel, func(b []byte) []byte {
			f := wheelFiles(t, b)
			info := strings.TrimSuffix(strings.TrimPrefix(wheel, "wheels/"), "-py3-none-any.whl") + ".dist-info/RECORD"
			rows, _ := csv.NewReader(bytes.NewReader(f[info])).ReadAll()
			for _, r := range rows {
				if r[0] == dist+"/__init__.py" {
					r[2] = size
				}
			}
			var rec bytes.Buffer
			w := csv.NewWriter(&rec)
			_ = w.WriteAll(rows)
			f[info] = rec.Bytes()
			var buf bytes.Buffer
			z := zip.NewWriter(&buf)
			for n, c := range f {
				zw, _ := z.Create(n)
				_, _ = zw.Write(c)
			}
			_ = z.Close()
			return buf.Bytes()
		})
		_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), "")
		if err == nil || !strings.Contains(err.Error(), "RECORD") {
			t.Fatalf("size %q: %v", size, err)
		}
	}
}
