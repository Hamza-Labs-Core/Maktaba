package fppackage

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Story 54.45 §3: a store package of a Maktaba plugin, made here from the bundle this binary
// embeds the way tools/plugins/build.py makes one, signed with test keys (public labels,
// never a real key).

const serverVersion = "0.9.0"

func testSigner(label string) pluginsig.Signer {
	seed := sha256.Sum256([]byte("maktaba-plugins TEST KEY, never for production: " + label))
	return pluginsig.NewSigner(seed[:])
}

type kit struct {
	root, store, maktaba, other pluginsig.Signer
	now                         time.Time
}

func newKit() kit {
	return kit{root: testSigner("root-1"), store: testSigner("store-1"), maktaba: testSigner("maktaba-developer"),
		other: testSigner("developer-2"), now: time.Now().UTC().Truncate(time.Second)}
}

// storeKeys is a verified store-keys.json listing the kit's store key.
func (k kit) storeKeys(t *testing.T) *pluginsig.StoreKeys {
	t.Helper()
	rootRaw, err := pluginsig.SignEnvelope(pluginsig.KindRoot, pluginsig.RootSigned{Version: 1, Threshold: 1,
		Keys: []pluginsig.RootKey{{ID: k.root.ID, Public: k.root.Public()}}}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	root, err := pluginsig.ParseRoot(rootRaw)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pluginsig.SignEnvelope(pluginsig.KindStoreKeys, pluginsig.StoreKeysSigned{Version: 1,
		Expires: pluginsig.FormatTime(k.now.Add(300 * 24 * time.Hour)),
		Keys: []pluginsig.StoreKey{{ID: k.store.ID, Public: k.store.Public(), Use: pluginsig.UseStore,
			NotBefore: pluginsig.FormatTime(k.now.Add(-30 * 24 * time.Hour)), NotAfter: pluginsig.FormatTime(k.now.Add(365 * 24 * time.Hour))}}}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := root.VerifyStoreKeys(raw, pluginsig.State{}, k.now)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// stamp sets the manifest's version and maktaba lines, as build.py does.
func stamp(manifest, version string) string {
	manifest = regexp.MustCompile(`(?m)^version(\s*)=(\s*)"[^"\n]*"`).ReplaceAllString(manifest, `version${1}=${2}"`+version+`"`)
	return regexp.MustCompile(`(?m)^maktaba(\s*)=(\s*)"[^"\n]*"`).ReplaceAllString(manifest, `maktaba${1}=${2}"=`+version+`"`)
}

func recordHash(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256=" + base64.RawURLEncoding.EncodeToString(s[:])
}

// wheel is a pure wheel of files (path -> bytes) for dist at version, its RECORD last.
func wheel(t *testing.T, dist, version string, files map[string][]byte) []byte {
	t.Helper()
	info := dist + "-" + version + ".dist-info/"
	all := map[string][]byte{}
	for k, v := range files {
		all[k] = v
	}
	all[info+"METADATA"] = []byte("Metadata-Version: 2.1\nName: " + strings.ReplaceAll(dist, "_", "-") + "\nVersion: " + version +
		"\nSummary: test\nLicense: AGPL-3.0-only\nRequires-Python: >=3.12\nRequires-Dist: maktaba-pipeline==" + version + "\n")
	all[info+"WHEEL"] = []byte("Wheel-Version: 1.0\nGenerator: test\nRoot-Is-Purelib: true\nTag: py3-none-any\n")
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	var rec bytes.Buffer
	w := csv.NewWriter(&rec)
	for _, n := range names {
		_ = w.Write([]string{n, recordHash(all[n]), fmt.Sprint(len(all[n]))})
	}
	_ = w.Write([]string{info + "RECORD", "", ""})
	w.Flush()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, n := range append(names, info+"RECORD") {
		f, _ := z.Create(n)
		if n == info+"RECORD" {
			_, _ = f.Write(rec.Bytes())
		} else {
			_, _ = f.Write(all[n])
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type pkgFiles map[string][]byte

// plugin is a package of the embedded plugin id at version, as files.
func plugin(t *testing.T, id, version string) pkgFiles {
	t.Helper()
	var doc struct {
		Plugins []struct {
			ID         string                       `json:"id"`
			Manifest   string                       `json:"manifest"`
			Strings    map[string]map[string]string `json:"strings"`
			Statements json.RawMessage              `json:"statements"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(sharedplugins.FirstParty, &doc); err != nil {
		t.Fatal(err)
	}
	e := ByID(id)
	for _, p := range doc.Plugins {
		if p.ID != id {
			continue
		}
		en, _ := json.Marshal(p.Strings["en"])
		ar, _ := json.Marshal(p.Strings["ar"])
		return pkgFiles{
			"manifest.toml":   []byte(stamp(p.Manifest, version)),
			"i18n/en.json":    en,
			"i18n/ar.json":    ar,
			"statements.json": p.Statements,
			"wheels/" + e.Dist() + "-" + version + "-py3-none-any.whl": wheel(t, e.Dist(), version,
				map[string][]byte{e.Dist() + "/__init__.py": []byte("")}),
		}
	}
	t.Fatalf("no %s in the bundle", id)
	return nil
}

// filesJSON is the package's files.json (manifest.toml included, sorted by path).
func (f pkgFiles) filesJSON(t *testing.T) []byte {
	t.Helper()
	type entry struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	paths := make([]string, 0, len(f))
	for p := range f {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	list := make([]entry, 0, len(paths))
	for _, p := range paths {
		s := sha256.Sum256(f[p])
		list = append(list, entry{Path: p, SHA256: hex.EncodeToString(s[:]), Size: int64(len(f[p]))})
	}
	b, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// unpacked writes the package's files as componentpack.UnpackPlugin would.
func (f pkgFiles) unpacked(t *testing.T) Unpacked {
	t.Helper()
	dir := t.TempDir()
	u := Unpacked{Dir: dir, Manifest: f["manifest.toml"]}
	for p, b := range f {
		full := filepath.Join(dir, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
		u.Files = append(u.Files, p)
	}
	return u
}

// signed is the package's signatures: developer.sig by dev, store.sig by the kit's store key
// naming dev (store false: none).
func (k kit) signed(t *testing.T, f pkgFiles, id, version string, dev pluginsig.Signer, store bool, level string) Signed {
	t.Helper()
	fj := f.filesJSON(t)
	out := Signed{FilesJSON: fj}
	d, err := pluginsig.SignDeveloper(dev, id, version, fj)
	if err != nil {
		t.Fatal(err)
	}
	out.DeveloperSig = d
	if store {
		sum, _ := pluginsig.FilesSHA256(fj)
		e := ByID(id)
		if e == nil {
			e = ByID(subtitles) // an id this server has no plugin for: the capabilities it carries
		}
		caps, _ := pluginsig.CapabilitiesSHA256(e.Manifest["capabilities"])
		s, err := pluginsig.SignStore(k.store, pluginsig.StoreSigned{PluginID: id, Version: version, FilesSHA256: sum,
			DeveloperKeyID: dev.ID, ReviewLevel: level, ReviewedAt: pluginsig.FormatTime(k.now.Add(-time.Hour)), CapabilitiesSHA256: caps})
		if err != nil {
			t.Fatal(err)
		}
		out.StoreSig = s
	}
	return out
}

// testPipeline is the pipeline wheel's RECORD for subtitles' code, as the kit's packages
// carry it.
func testPipeline(t *testing.T) map[string]string {
	t.Helper()
	f := plugin(t, subtitles, serverVersion)
	e := ByID(subtitles)
	rec, _, _, err := ReadWheel(f["wheels/"+e.Dist()+"-"+serverVersion+"-py3-none-any.whl"])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for p, d := range rec {
		if strings.HasPrefix(p, e.Dist()+"/") {
			out[p] = d
		}
	}
	return out
}

func switchable(component string) bool { return !strings.HasPrefix(component, "core.") }

func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

const subtitles = "tv.maktaba.subtitles"

func TestAStorePackageOfAMaktabaPluginIsTrusted(t *testing.T) {
	k := newKit()
	defer UseMaktabaKeys(k.maktaba.ID)()
	f := plugin(t, subtitles, serverVersion)
	for _, level := range []string{pluginsig.LevelAutomated, pluginsig.LevelReviewed} {
		res, err := CheckStore(f.unpacked(t), k.signed(t, f, subtitles, serverVersion, k.maktaba, true, level), k.storeKeys(t), serverVersion, switchable, testPipeline(t))
		if err != nil || res.DeveloperKeyID != k.maktaba.ID || res.Level != level {
			t.Fatalf("level %s: %+v %v", level, res, err)
		}
	}
}

// Every refusal of §3 is plugin_trusted_unsigned (another version, plugin_trusted_version).
func TestEveryRefusalOfTheStoreRule(t *testing.T) {
	k := newKit()
	defer UseMaktabaKeys(k.maktaba.ID)()
	keys := k.storeKeys(t)
	good := plugin(t, subtitles, serverVersion)
	for _, c := range []struct {
		name string
		run  func() error
		code string
	}{
		{"no store signature", func() error {
			_, err := CheckStore(good.unpacked(t), k.signed(t, good, subtitles, serverVersion, k.maktaba, false, ""), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"no store-keys.json at the home", func() error {
			_, err := CheckStore(good.unpacked(t), k.signed(t, good, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), nil, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"another developer's key", func() error {
			_, err := CheckStore(good.unpacked(t), k.signed(t, good, subtitles, serverVersion, k.other, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"no pinned key (this build's list is empty)", func() error {
			restore := UseMaktabaKeys()
			defer restore()
			_, err := CheckStore(good.unpacked(t), k.signed(t, good, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"other statements, signed by Maktaba and the store", func() error {
			f := plugin(t, subtitles, serverVersion)
			var sts []map[string]any
			if err := json.Unmarshal(f["statements.json"], &sts); err != nil || len(sts) == 0 {
				t.Fatalf("statements: %v", err)
			}
			sts[0]["text"] = "/* more */ " + fmt.Sprint(sts[0]["text"])
			f["statements.json"], _ = json.Marshal(sts)
			_, err := CheckStore(f.unpacked(t), k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"a wider manifest, signed by Maktaba and the store", func() error {
			f := plugin(t, subtitles, serverVersion)
			f["manifest.toml"] = []byte(strings.Replace(string(f["manifest.toml"]), "[trusted]\n", "[trusted]\nnet = [\"example.com\"]\n", 1))
			_, err := CheckStore(f.unpacked(t), k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"a file signed after it changed", func() error {
			f := plugin(t, subtitles, serverVersion)
			sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
			sig.FilesJSON = append([]byte(nil), sig.FilesJSON...)
			sig.FilesJSON[5] ^= 1
			_, err := CheckStore(f.unpacked(t), sig, keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"no pipeline wheel RECORD at the home", func() error {
			_, err := CheckStore(good.unpacked(t), k.signed(t, good, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, nil)
			return err
		}, CodeTrustedUnsigned},
		{"code rebuilt from other source, signed by Maktaba and the store", func() error {
			f := plugin(t, subtitles, serverVersion)
			e := ByID(subtitles)
			f["wheels/"+e.Dist()+"-"+serverVersion+"-py3-none-any.whl"] = wheel(t, e.Dist(), serverVersion,
				map[string][]byte{e.Dist() + "/__init__.py": []byte("import os\n")})
			_, err := CheckStore(f.unpacked(t), k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"other strings, signed by Maktaba and the store", func() error {
			f := plugin(t, subtitles, serverVersion)
			var words map[string]string
			if err := json.Unmarshal(f["i18n/ar.json"], &words); err != nil || len(words) == 0 {
				t.Fatalf("strings: %v", err)
			}
			for w := range words {
				words[w] = "!" + words[w]
				break
			}
			f["i18n/ar.json"], _ = json.Marshal(words)
			_, err := CheckStore(f.unpacked(t), k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"a file on disk other than the signed one", func() error {
			// the same words with other bytes: equal to the bundle's, but not what was signed
			f := plugin(t, subtitles, serverVersion)
			sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
			u := f.unpacked(t)
			if err := os.WriteFile(filepath.Join(u.Dir, "i18n", "en.json"), append([]byte(" "), f["i18n/en.json"]...), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := CheckStore(u, sig, keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"core's plugin", func() error {
			f := plugin(t, "tv.maktaba.core-media", serverVersion)
			_, err := CheckStore(f.unpacked(t), k.signed(t, f, "tv.maktaba.core-media", serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedUnsigned},
		{"another server's version", func() error {
			f := plugin(t, subtitles, "0.8.9")
			_, err := CheckStore(f.unpacked(t), k.signed(t, f, subtitles, "0.8.9", k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t))
			return err
		}, CodeTrustedVersion},
	} {
		err := c.run()
		if codeOf(err) != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	// a third-party id, and an id this server has no plugin for
	for _, id := range []string{"dev.example.subtitles", "tv.maktaba.unknown"} {
		f := plugin(t, subtitles, serverVersion)
		f["manifest.toml"] = []byte(strings.Replace(string(f["manifest.toml"]), `"`+subtitles+`"`, `"`+id+`"`, 1))
		if _, err := CheckStore(f.unpacked(t), k.signed(t, f, id, serverVersion, k.maktaba, true, pluginsig.LevelReviewed), keys, serverVersion, switchable, testPipeline(t)); codeOf(err) != CodeTrustedUnsigned {
			t.Errorf("%s: %v", id, err)
		}
	}
}

// The embedded list of Maktaba's keys reads, and is empty until the key is made: no store
// package of a Maktaba plugin is trusted before then.
func TestTheEmbeddedKeysAreEmptyUntilTheKeyIsMade(t *testing.T) {
	ids, err := MaktabaKeyIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("pinned keys %v: a real key is a release decision, with this test changed in the same PR", ids)
	}
}

// Every embedded plugin reads from the bundle; core's component is not switchable.
func TestTheBundleReads(t *testing.T) {
	l, err := Bundle()
	if err != nil || len(l) == 0 {
		t.Fatalf("%v %v", len(l), err)
	}
	if e := ByID("tv.maktaba.core-media"); e == nil || !strings.HasPrefix(e.Component, "core.") {
		t.Fatalf("core's plugin: %+v", e)
	}
}

// #1458 item 2: parseBundle refuses duplicate plugin ids and a manifest whose id differs
// from its bundle entry id.
func TestParseBundleRefusesDuplicateAndMismatchedIDs(t *testing.T) {
	var doc struct {
		Schema  int               `json:"schema"`
		Digest  string            `json:"digest"`
		Plugins []json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(sharedplugins.FirstParty, &doc); err != nil || len(doc.Plugins) == 0 {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	// Duplicate plugin entry with the same id.
	dupDoc := map[string]any{
		"schema":  doc.Schema,
		"digest":  doc.Digest,
		"plugins": []json.RawMessage{doc.Plugins[0], doc.Plugins[0]},
	}
	dupRaw, err := json.Marshal(dupDoc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseBundle(dupRaw); err == nil || !strings.Contains(err.Error(), "twice or not its own") {
		t.Fatalf("duplicate id in bundle: got %v, want error", err)
	}

	// Bundle entry whose top-level id does not match its manifest's id.
	var first map[string]any
	if err := json.Unmarshal(doc.Plugins[0], &first); err != nil {
		t.Fatal(err)
	}
	first["id"] = "tv.maktaba.other-id"
	mismatchRaw, err := json.Marshal(map[string]any{
		"schema":  doc.Schema,
		"digest":  doc.Digest,
		"plugins": []any{first},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseBundle(mismatchRaw); err == nil || !strings.Contains(err.Error(), "twice or not its own") {
		t.Fatalf("mismatched manifest id in bundle: got %v, want error", err)
	}
}

// #1458 item 3: CheckStore cross-checks Unpacked.Files and Unpacked.Manifest against the
// signed files.json and verified manifest.toml on disk.
func TestCheckStoreCrossChecksSignedFilesAndManifest(t *testing.T) {
	k := newKit()
	defer UseMaktabaKeys(k.maktaba.ID)()
	keys := k.storeKeys(t)
	pipe := testPipeline(t)

	// A package with a sixth file in signed files.json, while the caller passes only the five
	// expected paths in Unpacked.Files.
	t.Run("sixth file in signed files.json omitted from Unpacked.Files", func(t *testing.T) {
		f := plugin(t, subtitles, serverVersion)
		f["extra.txt"] = []byte("sneaky sixth file")
		sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
		u := f.unpacked(t)
		filtered := make([]string, 0, len(u.Files))
		for _, p := range u.Files {
			if p != "extra.txt" {
				filtered = append(filtered, p)
			}
		}
		u.Files = filtered
		if _, err := CheckStore(u, sig, keys, serverVersion, switchable, pipe); codeOf(err) != CodeTrustedUnsigned {
			t.Fatalf("sixth file omitted from Unpacked.Files: %v, want %s", err, CodeTrustedUnsigned)
		}
	})

	// Caller adds an extra path in Unpacked.Files that is not in signed files.json.
	t.Run("extra file in Unpacked.Files not in signed files.json", func(t *testing.T) {
		f := plugin(t, subtitles, serverVersion)
		sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
		u := f.unpacked(t)
		u.Files = append(u.Files, "ghost.txt")
		if _, err := CheckStore(u, sig, keys, serverVersion, switchable, pipe); codeOf(err) != CodeTrustedUnsigned {
			t.Fatalf("extra file in Unpacked.Files: %v, want %s", err, CodeTrustedUnsigned)
		}
	})

	// Caller omits a signed path from Unpacked.Files.
	t.Run("signed file missing from Unpacked.Files", func(t *testing.T) {
		f := plugin(t, subtitles, serverVersion)
		sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
		u := f.unpacked(t)
		u.Files = u.Files[:len(u.Files)-1]
		if _, err := CheckStore(u, sig, keys, serverVersion, switchable, pipe); codeOf(err) != CodeTrustedUnsigned {
			t.Fatalf("missing file in Unpacked.Files: %v, want %s", err, CodeTrustedUnsigned)
		}
	})

	// Caller supplies a different Manifest buffer than the signed manifest.toml on disk.
	t.Run("Unpacked.Manifest differs from signed manifest.toml", func(t *testing.T) {
		f := plugin(t, subtitles, serverVersion)
		sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
		u := f.unpacked(t)
		u.Manifest = append([]byte("\n"), u.Manifest...)
		if _, err := CheckStore(u, sig, keys, serverVersion, switchable, pipe); codeOf(err) != CodeTrustedUnsigned {
			t.Fatalf("mismatched Unpacked.Manifest: %v, want %s", err, CodeTrustedUnsigned)
		}
	})
}

// #1456 item 1: statements.json rejects both unknown extra fields and duplicate object keys
// (even when the last value of the duplicated key matches the embedded statement).
func TestStatementsRejectsDuplicateKeysAndExtraFields(t *testing.T) {
	k := newKit()
	defer UseMaktabaKeys(k.maktaba.ID)()
	keys := k.storeKeys(t)
	pipe := testPipeline(t)

	t.Run("duplicate key whose last value matches", func(t *testing.T) {
		f := plugin(t, subtitles, serverVersion)
		orig := string(f["statements.json"])
		// Inject a duplicate "mode" key right before the real "mode" key so the last value matches.
		mutated := strings.Replace(orig, `"mode":`, `"mode":"evil","mode":`, 1)
		if mutated == orig {
			t.Fatalf("failed to inject duplicate key into %s", orig)
		}
		f["statements.json"] = []byte(mutated)
		sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
		if _, err := CheckStore(f.unpacked(t), sig, keys, serverVersion, switchable, pipe); codeOf(err) != CodeTrustedUnsigned {
			t.Fatalf("duplicate key in statements.json: %v, want %s", err, CodeTrustedUnsigned)
		}
	})

	t.Run("extra unknown field", func(t *testing.T) {
		f := plugin(t, subtitles, serverVersion)
		var sts []map[string]any
		if err := json.Unmarshal(f["statements.json"], &sts); err != nil || len(sts) == 0 {
			t.Fatalf("statements: %v", err)
		}
		sts[0]["extra_field"] = "unexpected"
		f["statements.json"], _ = json.Marshal(sts)
		sig := k.signed(t, f, subtitles, serverVersion, k.maktaba, true, pluginsig.LevelReviewed)
		if _, err := CheckStore(f.unpacked(t), sig, keys, serverVersion, switchable, pipe); codeOf(err) != CodeTrustedUnsigned {
			t.Fatalf("extra field in statements.json: %v, want %s", err, CodeTrustedUnsigned)
		}
	})

	t.Run("checkNoDuplicateKeys catches nested duplicate keys", func(t *testing.T) {
		if err := checkNoDuplicateKeys([]byte(`[{"outer":{"a":1,"a":1}}]`)); err == nil {
			t.Fatal("nested duplicate key was accepted")
		}
		if err := checkNoDuplicateKeys([]byte(`[{"a":1}] {"trailing":true}`)); err == nil {
			t.Fatal("trailing JSON value was accepted")
		}
	})
}
