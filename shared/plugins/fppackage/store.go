package fppackage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

var (
	keysMu   sync.Mutex
	keysOver []string
	keysSet  bool
)

// MaktabaKeyIDs answers the developer keys of Maktaba's plugins this binary pins
// (shared/plugins/maktaba-keys.json, story 54.45 §3). None until the key is made.
func MaktabaKeyIDs() ([]string, error) {
	keysMu.Lock()
	defer keysMu.Unlock()
	if keysSet {
		return append([]string(nil), keysOver...), nil
	}
	var doc struct {
		DeveloperKeys []string `json:"developer_keys"`
	}
	if err := json.Unmarshal(sharedplugins.MaktabaKeys, &doc); err != nil {
		return nil, fmt.Errorf("fppackage: maktaba-keys.json: %w", err)
	}
	return doc.DeveloperKeys, nil
}

// UseMaktabaKeys pins ids instead of the embedded list (tests); the func restores what was
// pinned before.
func UseMaktabaKeys(ids ...string) func() {
	keysMu.Lock()
	prev, prevSet := keysOver, keysSet
	keysOver, keysSet = ids, true
	keysMu.Unlock()
	return func() {
		keysMu.Lock()
		keysOver, keysSet = prev, prevSet
		keysMu.Unlock()
	}
}

// Signed is what a home has of a package's signatures: its files.json and the two .sig
// files (componentpack.PluginPackage's).
type Signed struct {
	FilesJSON    []byte
	DeveloperSig []byte
	StoreSig     []byte
}

// CheckStore is story 54.45 §3: a package of a Maktaba plugin delivered apart from the
// release is trusted when
//
//   - its id is a plugin of this server's bundle, and one with a switch (switchable answers
//     its [trusted] component: core is not a plugin and is never installed, 54.44);
//   - the store countersigned it (store.sig verifies against keys, review level automated
//     or reviewed) and its developer.sig is by a key this binary pins as Maktaba's;
//   - its contents are exactly this server's plugin at this server's version
//     (CheckContents): the same manifest, strings and statements. A Maktaba plugin's
//     statements change only with a server release, so a package bringing any other SQL is
//     refused;
//   - its wheel is the plugin's code in this server's own pipeline wheel, digest for digest
//     both ways (pipeline: that wheel's RECORD, path -> "sha256=..."). Until the code leaves
//     the pipeline wheel (54.49) every home has it, so a package rebuilt from other source
//     is refused though Maktaba's key signed it.
//
// Every file is read again against the signed files.json, so the check does not rest on
// the caller's unpacking. Anything else is plugin_trusted_unsigned (a version other than
// the server's, plugin_trusted_version), and nothing of it may run. keys is the home's
// verified store-keys.json (nil: none, which trusts nothing); a nil pipeline trusts
// nothing either.
func CheckStore(u Unpacked, sig Signed, keys *pluginsig.StoreKeys, serverVersion string, switchable func(component string) bool, pipeline map[string]string) (*pluginsig.Result, error) {
	m, err := manifestID(u.Manifest)
	if err != nil {
		return nil, unsigned("%v", err)
	}
	id, version := m.ID, m.Version
	if !strings.HasPrefix(id, Prefix) {
		return nil, unsigned("%s is not a Maktaba id", id)
	}
	e := ByID(id)
	if e == nil {
		return nil, unsigned("%s is not one of this server's plugins", id)
	}
	if switchable == nil || !switchable(e.Component) {
		return nil, unsigned("%s is core, not a plugin: it is never installed apart from the server", id)
	}
	if keys == nil {
		return nil, unsigned("%s: the home has no store-keys.json to check the store's signature with", id)
	}
	if pipeline == nil {
		return nil, unsigned("%s: no pipeline wheel RECORD to compare its code with", id)
	}
	if len(sig.StoreSig) == 0 {
		return nil, unsigned("%s %s is not countersigned by the store", id, version)
	}
	caps, err := pluginsig.CapabilitiesSHA256(e.Manifest["capabilities"])
	if err != nil {
		return nil, unsigned("capabilities: %v", err)
	}
	res, err := pluginsig.VerifyPackage(pluginsig.Package{FilesJSON: sig.FilesJSON, DeveloperSig: sig.DeveloperSig, StoreSig: sig.StoreSig,
		PluginID: id, Version: version, CapabilitiesSHA256: caps, Open: u.open}, keys)
	if err != nil {
		return nil, unsigned("%s %s: %v", id, version, err)
	}
	if res.Level != pluginsig.LevelAutomated && res.Level != pluginsig.LevelReviewed {
		return nil, unsigned("%s %s is not countersigned by the store (level %s)", id, version, res.Level)
	}
	pinned, err := MaktabaKeyIDs()
	if err != nil {
		return nil, unsigned("%v", err)
	}
	ok := false
	for _, k := range pinned {
		ok = ok || k == res.DeveloperKeyID
	}
	if !ok {
		return nil, unsigned("%s %s is signed by %s, which is not Maktaba's key", id, version, res.DeveloperKeyID)
	}
	signedPaths, err := verifySignedMembers(u, sig.FilesJSON)
	if err != nil {
		return nil, err
	}
	diskManifest, ok := u.read("manifest.toml")
	if !ok || !bytes.Equal(u.Manifest, diskManifest) {
		return nil, unsigned("%s %s: manifest does not match the signed manifest.toml", id, version)
	}
	verified := Unpacked{Dir: u.Dir, Manifest: diskManifest, Files: signedPaths}
	if _, err := CheckContents(verified, e, serverVersion, pipeline); err != nil {
		return nil, err
	}
	return res, nil
}

// verifySignedMembers checks that the caller's Unpacked.Files matches the signed
// files.json member set exactly, with no duplicates, omissions, or extras.
func verifySignedMembers(u Unpacked, filesJSON []byte) ([]string, error) {
	var entries []pluginsig.FileEntry
	if err := json.Unmarshal(filesJSON, &entries); err != nil {
		return nil, unsigned("files.json: %v", err)
	}
	signedSet := make(map[string]bool, len(entries))
	signedPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if signedSet[entry.Path] {
			return nil, unsigned("files.json lists %s more than once", entry.Path)
		}
		signedSet[entry.Path] = true
		signedPaths = append(signedPaths, entry.Path)
	}
	callerSet := make(map[string]bool, len(u.Files))
	for _, f := range u.Files {
		if callerSet[f] {
			return nil, unsigned("unpacked file list names %s more than once", f)
		}
		if !signedSet[f] {
			return nil, unsigned("unpacked file %s is not in signed files.json", f)
		}
		callerSet[f] = true
	}
	for _, p := range signedPaths {
		if !callerSet[p] {
			return nil, unsigned("signed files.json lists %s, missing from unpacked file list", p)
		}
	}
	return signedPaths, nil
}

// manifestID reads a manifest's id and version only (the full check is CheckContents').
func manifestID(src []byte) (struct{ ID, Version string }, error) {
	var out struct{ ID, Version string }
	var m struct {
		ID      string `toml:"id"`
		Version string `toml:"version"`
	}
	if _, err := tomlDecode(src, &m); err != nil {
		return out, fmt.Errorf("manifest: %w", err)
	}
	out.ID, out.Version = m.ID, m.Version
	return out, nil
}
