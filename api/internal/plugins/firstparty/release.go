package firstparty

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/statements"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// The release's first-party packages (story 54.27 §2): tools/plugins/build.py writes, per
// plugin of the bundle, <id>-<version>.mkplugin and its files.json beside it as
// <id>-<version>.files.json; the release lists both in its signed checksums.txt.

// PackageName is the release file of a first-party package.
func PackageName(id, version string) string { return id + "-" + version + ".mkplugin" }

// FilesJSONName is the release file holding a package's files.json.
func FilesJSONName(id, version string) string { return id + "-" + version + ".files.json" }

// VerifyReleasePackages is story 54.27 T01: in a release directory whose checksums.txt is
// sums, every plugin of the embedded bundle has its package and its files.json, both listed
// under their own names with their own digests; each package unpacks with the home's reader
// (componentpack.UnpackPlugin) to that files.json and holds exactly its manifest, strings,
// statements and one wheel of its own; its manifest is the bundle's, stamped with this
// version (version, maktaba "=<version>") and valid as a trusted manifest; its strings and
// statements are the ones the server embeds; its wheel's files match the wheel's RECORD and,
// when pipelineWheel names the release's signed pipeline wheel (the code the runner loads),
// equal that wheel's files of the plugin's package, file for file; and CheckPackage accepts
// it. No other first-party package or files.json may be there, nor listed in the checksums.
// It answers the packages checked, sorted.
func VerifyReleasePackages(dir, version string, sums releaseverify.Checksums, pipelineWheel string) ([]string, error) {
	plugins, err := Plugins()
	if err != nil {
		return nil, err
	}
	if len(plugins) == 0 {
		return nil, fmt.Errorf("plugins: the bundle has no first-party plugin")
	}
	// a pre-release (1.2.3-rc.1) carries no first-party package: a manifest's maktaba range
	// takes only MAJOR.MINOR.PATCH (54.01 §2), so tools/plugins/build.py builds none, and
	// none may be there
	if strings.Contains(version, "-") {
		if n := firstPartyNames(dir, sums); n != "" {
			return nil, &Error{Code: CodeTrustedUnsigned, Detail: n + ": a pre-release carries no first-party package"}
		}
		return nil, nil
	}
	var pipeline map[string]string
	if pipelineWheel != "" {
		b, err := listed(dir, pipelineWheel, sums)
		if err != nil {
			return nil, err
		}
		if pipeline, err = wheelRecord(b); err != nil {
			return nil, fmt.Errorf("%s: %w", pipelineWheel, err)
		}
	}
	want := map[string]bool{}
	var out []string
	for _, fp := range plugins {
		name := PackageName(fp.ID, version)
		want[name], want[FilesJSONName(fp.ID, version)] = true, true
		if err := verifyOne(dir, version, fp, sums, pipeline); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, name)
	}
	// nothing else that looks like a first-party package, on disk or in the checksums: a
	// signed files.json of no bundle plugin would trust a package this server never embedded
	if n := firstPartyNames(dir, sums, want); n != "" {
		return nil, &Error{Code: CodeTrustedUnsigned, Detail: n + ": no plugin of this release's bundle"}
	}
	sort.Strings(out)
	return out, nil
}

// firstPartyNames answers the first name, on disk in dir or in sums, that looks like a
// first-party package or its files.json and is not in want ("" when none).
func firstPartyNames(dir string, sums releaseverify.Checksums, want ...map[string]bool) string {
	names := sums.Names()
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		looks := strings.HasPrefix(n, FirstPartyPrefix) && (strings.HasSuffix(n, ".mkplugin") || strings.HasSuffix(n, ".files.json"))
		if !looks {
			continue
		}
		if len(want) == 0 || !want[0][n] {
			return n
		}
	}
	return ""
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// listed reads a release file and checks that checksums.txt names it with its digest.
func listed(dir, name string, sums releaseverify.Checksums) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	if got, ok := sums[name]; !ok || got != sha256Hex(b) {
		return nil, &Error{Code: CodeTrustedUnsigned, Detail: name + " is not in checksums.txt with its digest"}
	}
	return b, nil
}

func verifyOne(dir, version string, fp *FirstParty, sums releaseverify.Checksums, pipeline map[string]string) error {
	pkgBytes, err := listed(dir, PackageName(fp.ID, version), sums)
	if err != nil {
		return err
	}
	fj, err := listed(dir, FilesJSONName(fp.ID, version), sums)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "fp-verify-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	unpacked := filepath.Join(tmp, "p")
	pkg, err := componentpack.UnpackPlugin(bytes.NewReader(pkgBytes), unpacked, componentpack.PluginExpect{FilesSHA256: sha256Hex(fj)})
	if err != nil {
		return err
	}
	// exactly its manifest, strings, statements and its own wheel: nothing else rides along
	dist := "maktaba_fp_" + strings.ReplaceAll(fp.Folder, "-", "_")
	members := map[string]bool{}
	for _, f := range pkg.Files {
		members[f.Path] = true
	}
	read := func(rel string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(unpacked, filepath.FromSlash(rel)))
		return b, err == nil
	}
	m, errs := pluginmanifest.ValidateTrusted(pkg.Manifest, read)
	if len(errs) > 0 {
		return errs
	}
	id, _ := m["id"].(string)
	pv, _ := m["version"].(string)
	if id != fp.ID {
		return fmt.Errorf("its manifest names %q", id)
	}
	if m["maktaba"] != "="+version {
		return &Error{Code: CodeTrustedVersion, Detail: fmt.Sprintf("maktaba %v is not =%s", m["maktaba"], version)}
	}
	// a release version has no pre-release (VerifyReleasePackages), so its wheel's PEP 440
	// version is the same text
	wheel := "wheels/" + dist + "-" + version + "-py3-none-any.whl"
	expect := []string{"manifest.toml", "i18n/en.json", "i18n/ar.json", "statements.json", wheel}
	if len(members) != len(expect) {
		return fmt.Errorf("it holds %d files, not its manifest, strings, statements and its wheel %s", len(members), wheel)
	}
	for _, m := range expect {
		if !members[m] {
			return fmt.Errorf("it has no %s", m)
		}
	}
	// the manifest is the bundle's but for the two stamped fields
	stamped := map[string]any{}
	for k, v := range fp.Manifest {
		stamped[k] = v
	}
	stamped["version"], stamped["maktaba"] = m["version"], m["maktaba"]
	if !reflect.DeepEqual(stamped, m) {
		return fmt.Errorf("its manifest is not the one this server embeds")
	}
	for lang, words := range fp.Strings {
		b, ok := read("i18n/" + lang + ".json")
		var got map[string]string
		if !ok || json.Unmarshal(b, &got) != nil || !reflect.DeepEqual(got, words) {
			return fmt.Errorf("its i18n/%s.json is not the one this server embeds", lang)
		}
	}
	b, ok := read("statements.json")
	var sts []statements.Statement
	if !ok || json.Unmarshal(b, &sts) != nil || !reflect.DeepEqual(sts, fp.Statements) {
		return fmt.Errorf("its statements.json is not the one this server embeds")
	}
	whl, _ := read(wheel)
	if err := checkWheel(whl, dist, version, pipeline); err != nil {
		return fmt.Errorf("%s: %w", wheel, err)
	}
	return CheckPackage(Package{ID: id, Version: pv, FilesJSON: pkg.FilesJSON}, sums, version)
}

// wheelRecord reads a wheel's RECORD (PEP 376 CSV: path, sha256=<urlsafe base64>, size) and
// checks every member against it: each member listed with its digest, each listed path a
// member. It answers path -> digest (RECORD itself has none).
func wheelRecord(b []byte) (map[string]string, error) {
	rec, _, err := readWheel(b)
	return rec, err
}

// readWheel is wheelRecord, also answering each member's bytes.
func readWheel(b []byte) (map[string]string, map[string][]byte, error) {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, nil, err
	}
	var record *zip.File
	files := map[string]*zip.File{}
	for _, f := range z.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if _, dup := files[f.Name]; dup {
			return nil, nil, fmt.Errorf("%s twice", f.Name)
		}
		files[f.Name] = f
		if strings.HasSuffix(f.Name, ".dist-info/RECORD") && strings.Count(f.Name, "/") == 1 {
			if record != nil {
				return nil, nil, fmt.Errorf("two RECORD files")
			}
			record = f
		}
	}
	if record == nil {
		return nil, nil, fmt.Errorf("no RECORD")
	}
	rc, err := record.Open()
	if err != nil {
		return nil, nil, err
	}
	rows, err := csv.NewReader(rc).ReadAll()
	_ = rc.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("RECORD: %w", err)
	}
	out := map[string]string{}
	contents := map[string][]byte{}
	for _, r := range rows {
		if len(r) != 3 {
			return nil, nil, fmt.Errorf("RECORD: a row of %d fields", len(r))
		}
		path, digest := r[0], r[1]
		f, ok := files[path]
		if !ok {
			return nil, nil, fmt.Errorf("RECORD lists %s, which it does not hold", path)
		}
		if path == record.Name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, componentpack.PluginMaxUnpackedBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(data)
		size, err := strconv.ParseInt(r[2], 10, 64)
		if err != nil || size < 0 || size != int64(len(data)) || digest != "sha256="+base64.RawURLEncoding.EncodeToString(sum[:]) {
			return nil, nil, fmt.Errorf("%s does not match its RECORD", path)
		}
		out[path] = digest
		contents[path] = data
	}
	for name := range files {
		if _, ok := out[name]; !ok && name != record.Name {
			return nil, nil, fmt.Errorf("%s is not in RECORD", name)
		}
	}
	return out, contents, nil
}

// compiled is a file Python writes for itself, which a wheel may or may not carry.
func compiled(p string) bool {
	return strings.Contains(p, "/__pycache__/") || strings.HasSuffix(p, ".pyc")
}

// checkWheel: the plugin's wheel holds its own package (dist) and exactly its dist-info's
// METADATA, WHEEL and RECORD, each file as its RECORD says. Its METADATA names the plugin's
// distribution at this version and requires only maktaba-pipeline at this version, and its
// WHEEL says a pure py3-none-any wheel, so a package cannot pull another dependency in or
// claim another version. With the release's pipeline wheel's RECORD, its package's files
// equal that wheel's, digest for digest, both ways.
func checkWheel(b []byte, dist, version string, pipeline map[string]string) error {
	rec, files, err := readWheel(b)
	if err != nil {
		return err
	}
	info := dist + "-" + version + ".dist-info/"
	own := 0
	for p, d := range rec {
		switch {
		case p == info+"METADATA" || p == info+"WHEEL":
		case strings.HasPrefix(p, dist+"/"):
			own++
			if pipeline != nil {
				if pd, ok := pipeline[p]; !ok || pd != d {
					return fmt.Errorf("%s is not the release's pipeline wheel's", p)
				}
			}
		default:
			return fmt.Errorf("%s is outside its package %s and its dist-info", p, dist)
		}
	}
	if own == 0 {
		return fmt.Errorf("it holds no file of %s", dist)
	}
	meta, ok := files[info+"METADATA"]
	if !ok {
		return fmt.Errorf("no %sMETADATA", info)
	}
	name := strings.ReplaceAll(dist, "_", "-")
	if err := checkHeaders(meta, map[string]string{"Name": name, "Version": version, "Requires-Dist": "maktaba-pipeline==" + version},
		"Metadata-Version", "Summary", "License", "Requires-Python"); err != nil {
		return fmt.Errorf("METADATA: %w", err)
	}
	wh, ok := files[info+"WHEEL"]
	if !ok {
		return fmt.Errorf("no %sWHEEL", info)
	}
	if err := checkHeaders(wh, map[string]string{"Root-Is-Purelib": "true", "Tag": "py3-none-any"}, "Wheel-Version", "Generator"); err != nil {
		return fmt.Errorf("WHEEL: %w", err)
	}
	for p := range pipeline {
		if strings.HasPrefix(p, dist+"/") && !compiled(p) {
			if _, ok := rec[p]; !ok {
				return fmt.Errorf("the release's pipeline wheel has %s, which it lacks", p)
			}
		}
	}
	return nil
}

// checkHeaders reads "Key: value" lines: each key of exact once with that value, the free
// keys at most once each with any value, and no other key or line.
func checkHeaders(b []byte, exact map[string]string, free ...string) error {
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			return fmt.Errorf("the line %q", line)
		}
		if seen[k] {
			return fmt.Errorf("%s twice", k)
		}
		seen[k] = true
		if want, isExact := exact[k]; isExact {
			if v != want {
				return fmt.Errorf("%s is %q, not %q", k, v, want)
			}
			continue
		}
		allowed := false
		for _, f := range free {
			allowed = allowed || f == k
		}
		if !allowed {
			return fmt.Errorf("%s is not allowed", k)
		}
	}
	for k := range exact {
		if !seen[k] {
			return fmt.Errorf("no %s", k)
		}
	}
	return nil
}
