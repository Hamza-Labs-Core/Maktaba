package firstparty

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
// (componentpack.UnpackPlugin) to that files.json; its manifest is the bundle's, stamped
// with this version (version, maktaba "=<version>") and valid as a trusted manifest; its
// strings and statements are the ones the server embeds; and CheckPackage accepts it. No
// other first-party package may be there. It answers the packages checked, sorted.
func VerifyReleasePackages(dir, version string, sums releaseverify.Checksums) ([]string, error) {
	plugins, err := Plugins()
	if err != nil {
		return nil, err
	}
	if len(plugins) == 0 {
		return nil, fmt.Errorf("plugins: the bundle has no first-party plugin")
	}
	want := map[string]bool{}
	var out []string
	for _, fp := range plugins {
		name := PackageName(fp.ID, version)
		want[name] = true
		if err := verifyOne(dir, version, fp, sums); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, name)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), FirstPartyPrefix) && strings.HasSuffix(e.Name(), ".mkplugin") && !want[e.Name()] {
			return nil, fmt.Errorf("%s: no plugin of this release's bundle", e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
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

func verifyOne(dir, version string, fp *FirstParty, sums releaseverify.Checksums) error {
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
	return CheckPackage(Package{ID: id, Version: pv, FilesJSON: pkg.FilesJSON}, sums, version)
}
