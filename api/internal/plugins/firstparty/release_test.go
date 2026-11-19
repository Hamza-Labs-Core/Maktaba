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

	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/fppackage"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
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
	plugins := packaged(t)
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
	plugins := packaged(t)
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
	plugins := packaged(t)
	if len(plugins) == 0 {
		t.Fatal("no packaged plugin")
	}
	return plugins[0]
}

// packaged is the bundle's plugins a release packages: every one but core's (54.45 §1).
func packaged(t *testing.T) []*FirstParty {
	t.Helper()
	all, err := Plugins()
	if err != nil {
		t.Fatal(err)
	}
	var out []*FirstParty
	for _, fp := range all {
		if !IsCore(fp) {
			out = append(out, fp)
		}
	}
	return out
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
	plugins := packaged(t)
	for _, fp := range plugins {
		_, members := packageWheel(t, dir, fp.ID)
		for n, b := range members {
			if !strings.Contains(n, ".dist-info/") {
				files[n] = b
			}
		}
	}
	files["maktaba_pipeline-"+relVersion+".dist-info/METADATA"] = []byte("Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: " + relVersion + "\n")
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

// Third review of #1440: the pipeline wheel is this release's, by name and by its METADATA;
// an older one with the same plugin files is refused.
func TestAnotherReleasesPipelineWheelIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	name := pipelineWheel(t, dir, func(f map[string][]byte) {
		f["maktaba_pipeline-"+relVersion+".dist-info/METADATA"] = []byte("Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: 0.8.9\n")
	})
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), name); codeOf(err) != CodeTrustedVersion {
		t.Fatalf("an older pipeline wheel under this release's name: %v", err)
	}
	// the version on a line after the headers, or a header given twice: not this release's
	for what, meta := range map[string]string{
		"in the body":           "Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: 0.8.9\n\nVersion: " + relVersion + "\n",
		"only in the body":      "Metadata-Version: 2.1\nName: maktaba-pipeline\n\nVersion: " + relVersion + "\n",
		"given twice":           "Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: " + relVersion + "\nVersion: 0.8.9\n",
		"two versions":          "Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: " + relVersion + "\nVersion: " + relVersion + "\n",
		"another name":          "Metadata-Version: 2.1\nName: maktaba-pipeline\nName: evil\nVersion: " + relVersion + "\n",
		"names in two cases":    "Metadata-Version: 2.1\nname: not-maktaba-pipeline\nName: maktaba-pipeline\nVersion: " + relVersion + "\n",
		"versions in two cases": "Metadata-Version: 2.1\nName: maktaba-pipeline\nversion: 0.8.9\nVersion: " + relVersion + "\n",
		"a folded version":      "Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: " + relVersion + "\n 0.8.9\n",
		// Python reads a lone CR as a line end: "Version: 0.8.9" is a header to it
		"a version behind a CR": "Metadata-Version: 2.1\nName: maktaba-pipeline\nX-Note: x\rVersion: 0.8.9\nVersion: " + relVersion + "\n",
		"CRLF lines":            "Metadata-Version: 2.1\r\nName: maktaba-pipeline\r\nVersion: " + relVersion + "\r\n",
		"a line not a header":   "Metadata-Version: 2.1\nName: maktaba-pipeline\nnot a header\nVersion: " + relVersion + "\n",
		"a name with a space":   "Metadata-Version: 2.1\nName: maktaba-pipeline\nX Note: x\nVersion: " + relVersion + "\n",
		// Python reads "0.9.0 " here, not this release's version
		"a trailing blank": "Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: " + relVersion + " \n",
	} {
		name := pipelineWheel(t, dir, func(f map[string][]byte) {
			f["maktaba_pipeline-"+relVersion+".dist-info/METADATA"] = []byte(meta)
		})
		if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), name); codeOf(err) != CodeTrustedVersion {
			t.Fatalf("a pipeline wheel's version %s: %v", what, err)
		}
	}
	// a CR in the description's body, after the headers, is any text's: this release's wheel
	name = pipelineWheel(t, dir, func(f map[string][]byte) {
		f["maktaba_pipeline-"+relVersion+".dist-info/METADATA"] = []byte("Metadata-Version: 2.1\nName: maktaba-pipeline\nVersion: " + relVersion + "\n\nA line\r\nand a CR\r")
	})
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), name); err != nil {
		t.Fatalf("a CR in the description refused this release's wheel: %v", err)
	}
	// and a pipeline wheel named for another version
	name = pipelineWheel(t, dir, nil)
	other := "maktaba_pipeline-0.8.9-py3-none-any.whl"
	if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, other)); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), other); codeOf(err) != CodeTrustedVersion {
		t.Fatalf("a pipeline wheel of another version: %v", err)
	}
}

// Third review of #1440: the wheel's RECORD is the one in its own dist-info.
func TestAWheelWhoseRecordIsElsewhereIsRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	wheel, members := packageWheel(t, dir, fp.ID)
	info := strings.TrimSuffix(strings.TrimPrefix(wheel, "wheels/"), "-py3-none-any.whl") + ".dist-info"
	files := map[string][]byte{}
	for n, b := range members {
		if n != info+"/RECORD" {
			files[n] = b
		}
	}
	repack(t, dir, fp.ID, wheel, func([]byte) []byte { return wheelBytes(t, "other.dist-info", files) })
	_, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), "")
	if err == nil || !strings.Contains(err.Error(), "RECORD") {
		t.Fatalf("a RECORD outside its dist-info: %v", err)
	}
}

// Third review of #1440: a member over the unpacked limit is refused, never hashed as its
// first 200 MiB (two wheels sharing that prefix would read as equal).
func TestAnOversizedWheelMemberIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 200 MiB member")
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, err := z.CreateHeader(&zip.FileHeader{Name: "x/big.py", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := 0; i <= componentpack.PluginMaxUnpackedBytes>>20; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	rec, _ := z.Create("x-1.dist-info/RECORD")
	_, _ = rec.Write([]byte("x/big.py,sha256=AAAA,1\nx-1.dist-info/RECORD,,\n"))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := fppackage.ReadWheel(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("a member over the limit: %v", err)
	}
	// RECORD itself over the limit (its self-row skipped by the digest check): refused too
	buf.Reset()
	z = zip.NewWriter(&buf)
	w, err = z.CreateHeader(&zip.FileHeader{Name: "x-1.dist-info/RECORD", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("x-1.dist-info/RECORD,,\n"))
	blank := bytes.Repeat([]byte("\n"), 1<<20)
	for i := 0; i <= componentpack.PluginMaxUnpackedBytes>>20; i++ {
		_, _ = w.Write(blank)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := fppackage.ReadWheel(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("a RECORD over the limit: %v", err)
	}
}

// Fifth review of #1440: bytecode under a plugin's package is refused in either wheel (Python
// can run bytecode that is not its source). A wheel's limits in members and in total are
// fppackage's tests (TestAWheelsMembersAndTotalAreLimited).
func TestBytecodeAndAWheelsTotalAreRefused(t *testing.T) {
	dir := buildRelease(t, relVersion)
	fp := firstPlugin(t)
	dist := "maktaba_fp_" + strings.ReplaceAll(fp.Folder, "-", "_")
	for _, p := range []string{dist + "/__pycache__/__init__.cpython-312.pyc", dist + "/old.pyc"} {
		name := pipelineWheel(t, dir, func(f map[string][]byte) { f[p] = []byte("bytecode") })
		if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), name); err == nil || !strings.Contains(err.Error(), "bytecode") {
			t.Fatalf("%s in the pipeline wheel: %v", p, err)
		}
	}
	// the same in the package's own wheel
	wheel, members := packageWheel(t, dir, fp.ID)
	info := strings.TrimSuffix(strings.TrimPrefix(wheel, "wheels/"), "-py3-none-any.whl") + ".dist-info"
	files := map[string][]byte{}
	for n, b := range members {
		if n != info+"/RECORD" {
			files[n] = b
		}
	}
	files[dist+"/__pycache__/x.cpython-312.pyc"] = []byte("bytecode")
	repack(t, dir, fp.ID, wheel, func([]byte) []byte { return wheelBytes(t, info, files) })
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); err == nil || !strings.Contains(err.Error(), "bytecode") {
		t.Fatalf("bytecode in the package's wheel: %v", err)
	}
}

// A member whose zip entry declares fewer bytes than it holds is refused (the reader stops at
// the declared size and the entry fails), never read past its limit.
func TestAnUnderDeclaredMemberIsRefused(t *testing.T) {
	data := bytes.Repeat([]byte("z"), 4096)
	b := wheelBytes(t, "x-1.dist-info", map[string][]byte{"x/a.py": data})
	sig := []byte("PK\x01\x02") // a central directory entry
	patched := false
	for off := 0; ; {
		i := bytes.Index(b[off:], sig)
		if i < 0 {
			break
		}
		i += off
		nameLen := int(b[i+28]) | int(b[i+29])<<8
		if string(b[i+46:i+46+nameLen]) == "x/a.py" {
			b[i+24], b[i+25], b[i+26], b[i+27] = 10, 0, 0, 0 // its uncompressed size: 10
			patched = true
			break
		}
		off = i + 4
	}
	if !patched {
		t.Fatal("no central directory entry for x/a.py")
	}
	if _, _, _, err := fppackage.ReadWheel(b); err == nil {
		t.Fatal("a member holding more than it declares was read")
	}
}

// Story 54.45 §1: core is not a plugin. Its plugin is never packaged, and a package of it in a
// release is refused.
func TestCoreIsNotPackaged(t *testing.T) {
	all, _ := Plugins()
	var core *FirstParty
	for _, fp := range all {
		if IsCore(fp) {
			core = fp
		}
	}
	if core == nil || core.ID != "tv.maktaba.core-media" {
		t.Fatalf("core's plugin: %+v", core)
	}
	dir := buildRelease(t, relVersion)
	if _, err := os.Stat(filepath.Join(dir, PackageName(core.ID, relVersion))); !os.IsNotExist(err) {
		t.Fatalf("build.py packaged core: %v", err)
	}
	if len(packaged(t)) != len(all)-1 {
		t.Fatal("only core is left out")
	}
	// one made by hand under core's name is refused
	fp := firstPlugin(t)
	b, _ := os.ReadFile(filepath.Join(dir, PackageName(fp.ID, relVersion)))
	_ = os.WriteFile(filepath.Join(dir, PackageName(core.ID, relVersion)), b, 0o644)
	if _, err := VerifyReleasePackages(dir, relVersion, checksums(t, dir), ""); codeOf(err) != CodeTrustedUnsigned {
		t.Fatalf("a core package in a release: %v", err)
	}
}

// Story 54.45: a package built with Maktaba's developer key (build.py --developer-key), then
// countersigned by the store, is what a home trusts from the store (fppackage.CheckStore);
// the signature leaves files.json, and so the release's own check, unchanged.
func TestASignedPackageIsTrustedFromTheStore(t *testing.T) {
	if testing.Short() {
		t.Skip("builds packages")
	}
	seed := sha256.Sum256([]byte("maktaba-plugins TEST KEY, never for production: maktaba-developer"))
	keyFile := filepath.Join(t.TempDir(), "maktaba.key")
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(seed[:])), 0o600); err != nil {
		t.Fatal(err)
	}
	dev := pluginsig.NewSigner(seed[:])
	plain := buildRelease(t, relVersion)
	out := t.TempDir()
	cmd := exec.Command("python3", filepath.Join("..", "..", "..", "..", "tools", "plugins", "build.py"), "--mkplugin", packer(t),
		"--developer-key", keyFile, "--version", relVersion, "--out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build.py --developer-key: %v: %s", err, b)
	}
	if _, err := VerifyReleasePackages(out, relVersion, checksums(t, out), ""); err != nil {
		t.Fatalf("the release's check of signed packages: %v", err)
	}
	fp := firstPlugin(t)
	a, _ := os.ReadFile(filepath.Join(plain, FilesJSONName(fp.ID, relVersion)))
	b, _ := os.ReadFile(filepath.Join(out, FilesJSONName(fp.ID, relVersion)))
	if !bytes.Equal(a, b) {
		t.Fatal("signing changed files.json")
	}
	// the home: unpack, then the store rule with the store's countersignature
	f, err := os.Open(filepath.Join(out, PackageName(fp.ID, relVersion)))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "p")
	pkg, err := componentpack.UnpackPlugin(f, dir, componentpack.PluginExpect{})
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.DeveloperSig) == 0 {
		t.Fatal("no developer.sig in the package")
	}
	store, keys := storeKit(t)
	e := fppackage.ByID(fp.ID)
	sum, _ := pluginsig.FilesSHA256(pkg.FilesJSON)
	caps, _ := pluginsig.CapabilitiesSHA256(e.Manifest["capabilities"])
	ss, err := pluginsig.SignStore(store, pluginsig.StoreSigned{PluginID: fp.ID, Version: relVersion, FilesSHA256: sum, DeveloperKeyID: dev.ID,
		ReviewLevel: pluginsig.LevelReviewed, ReviewedAt: pluginsig.FormatTime(time.Now().UTC().Add(-time.Hour)), CapabilitiesSHA256: caps})
	if err != nil {
		t.Fatal(err)
	}
	files := []string{}
	for _, pf := range pkg.Files {
		files = append(files, pf.Path)
	}
	u := fppackage.Unpacked{Dir: dir, Manifest: pkg.Manifest, Files: files}
	switchable := func(c string) bool { cc, ok := components.Default().Get(c); return ok && cc.Switchable }
	sig := fppackage.Signed{FilesJSON: pkg.FilesJSON, DeveloperSig: pkg.DeveloperSig, StoreSig: ss}
	defer fppackage.UseMaktabaKeys(dev.ID)()
	if _, err := fppackage.CheckStore(u, sig, keys, relVersion, switchable); err != nil {
		t.Fatalf("a Maktaba-signed, store-countersigned package: %v", err)
	}
	// the same package under this build's real list (empty): refused
	restore := fppackage.UseMaktabaKeys()
	_, err = fppackage.CheckStore(u, sig, keys, relVersion, switchable)
	restore()
	if codeOf(err) != CodeTrustedUnsigned {
		t.Fatalf("no pinned key: %v", err)
	}
}

// storeKit is a test root, its store key and the verified store-keys.json listing it.
func storeKit(t *testing.T) (pluginsig.Signer, *pluginsig.StoreKeys) {
	t.Helper()
	mk := func(label string) pluginsig.Signer {
		s := sha256.Sum256([]byte("maktaba-plugins TEST KEY, never for production: " + label))
		return pluginsig.NewSigner(s[:])
	}
	root, store := mk("root-1"), mk("store-1")
	now := time.Now().UTC().Truncate(time.Second)
	rootRaw, err := pluginsig.SignEnvelope(pluginsig.KindRoot, pluginsig.RootSigned{Version: 1, Threshold: 1,
		Keys: []pluginsig.RootKey{{ID: root.ID, Public: root.Public()}}}, root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := pluginsig.ParseRoot(rootRaw)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pluginsig.SignEnvelope(pluginsig.KindStoreKeys, pluginsig.StoreKeysSigned{Version: 1, Expires: pluginsig.FormatTime(now.Add(300 * 24 * time.Hour)),
		Keys: []pluginsig.StoreKey{{ID: store.ID, Public: store.Public(), Use: pluginsig.UseStore,
			NotBefore: pluginsig.FormatTime(now.Add(-30 * 24 * time.Hour)), NotAfter: pluginsig.FormatTime(now.Add(365 * 24 * time.Hour))}}}, root)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := r.VerifyStoreKeys(raw, pluginsig.State{}, now)
	if err != nil {
		t.Fatal(err)
	}
	return store, keys
}
