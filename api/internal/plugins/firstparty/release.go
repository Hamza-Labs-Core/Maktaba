package firstparty

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/fppackage"
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
		// this release's pipeline wheel, by name and by its own METADATA: an older one with
		// the same plugin files would otherwise pass
		if want := "maktaba_pipeline-" + version + "-py3-none-any.whl"; pipelineWheel != want {
			return nil, &Error{Code: CodeTrustedVersion, Detail: pipelineWheel + " is not " + want}
		}
		b, err := listed(dir, pipelineWheel, sums)
		if err != nil {
			return nil, err
		}
		rec, files, recPath, err := fppackage.ReadWheel(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pipelineWheel, err)
		}
		info := "maktaba_pipeline-" + version + ".dist-info/"
		if recPath != info+"RECORD" {
			return nil, fmt.Errorf("%s: its RECORD is %s, not %sRECORD", pipelineWheel, recPath, info)
		}
		meta, ok := files[info+"METADATA"]
		if !ok || !fppackage.HasHeader(meta, "Name", "maktaba-pipeline") || !fppackage.HasHeader(meta, "Version", version) {
			return nil, &Error{Code: CodeTrustedVersion, Detail: pipelineWheel + ": its METADATA is not maktaba-pipeline " + version}
		}
		pipeline = rec
	}
	want := map[string]bool{}
	var out []string
	for _, fp := range plugins {
		// core is not a plugin (54.44): it stays in the server and its wheel and is never
		// packaged (54.45 §1); a package of it is refused below with the others
		if IsCore(fp) {
			continue
		}
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

// IsCore reports whether a first-party plugin is core: its [trusted] component is not
// switchable in the catalog (tv.maktaba.core-media, for core.pipeline). Core is not a plugin.
func IsCore(fp *FirstParty) bool {
	c, ok := components.Default().Get(fp.Trusted.Component)
	return ok && !c.Switchable
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
	e := fppackage.ByID(fp.ID)
	if e == nil {
		return &Error{Code: CodeTrustedUnsigned, Detail: fp.ID + " is not in the shared bundle"}
	}
	files := make([]string, 0, len(pkg.Files))
	for _, f := range pkg.Files {
		files = append(files, f.Path)
	}
	m, err := fppackage.CheckContents(fppackage.Unpacked{Dir: unpacked, Manifest: pkg.Manifest, Files: files}, e, version, pipeline)
	if err != nil {
		return err
	}
	id, _ := m["id"].(string)
	pv, _ := m["version"].(string)
	return CheckPackage(Package{ID: id, Version: pv, FilesJSON: pkg.FilesJSON}, sums, version)
}
