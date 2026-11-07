package plugins

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Story 54.14 T01, T02, T03 on the home: the supervisor's install checks the package's
// signatures against a test root (the vectors' keys: public labels, never a real root)
// and the store-keys.json the home holds, and refuses a hostile package at the right step
// with nothing left on disk.

func testSigner(label string) pluginsig.Signer {
	seed := sha256.Sum256([]byte("maktaba-plugins TEST KEY, never for production: " + label))
	return pluginsig.NewSigner(seed[:])
}

type trustKit struct {
	root, store1, store2, dev1, dev2 pluginsig.Signer
	now                              time.Time
}

func newKit() trustKit {
	return trustKit{root: testSigner("root-1"), store1: testSigner("store-1"), store2: testSigner("store-2"),
		dev1: testSigner("developer-1"), dev2: testSigner("developer-2"), now: time.Now().UTC().Truncate(time.Second)}
}

func (k trustKit) rootOf(t *testing.T) *pluginsig.Root {
	t.Helper()
	raw, err := pluginsig.SignEnvelope(pluginsig.KindRoot, pluginsig.RootSigned{Version: 1, Threshold: 1,
		Keys: []pluginsig.RootKey{{ID: k.root.ID, Public: k.root.Public()}}}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := pluginsig.ParseRoot(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (k trustKit) storeKeys(t *testing.T, version int64, keys ...pluginsig.Signer) []byte {
	t.Helper()
	list := []pluginsig.StoreKey{}
	for _, s := range keys {
		list = append(list, pluginsig.StoreKey{ID: s.ID, Public: s.Public(), Use: pluginsig.UseStore,
			NotBefore: pluginsig.FormatTime(k.now.Add(-30 * 24 * time.Hour)), NotAfter: pluginsig.FormatTime(k.now.Add(365 * 24 * time.Hour))})
	}
	raw, err := pluginsig.SignEnvelope(pluginsig.KindStoreKeys, pluginsig.StoreKeysSigned{Version: version,
		Expires: pluginsig.FormatTime(k.now.Add(300 * 24 * time.Hour)), Keys: list}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func pluginFiles(version string) map[string][]byte {
	return map[string][]byte{
		"manifest.toml": []byte(manifest(testID, version)),
		"i18n/en.json":  []byte(`{"plugin.name":"Hello","plugin.summary":"Says hello."}`),
		"i18n/ar.json":  []byte(`{"plugin.name":"مرحبا","plugin.summary":"يقول مرحبا."}`),
		"plugin.wasm":   []byte("\x00asm-not-really"),
	}
}

func filesJSONOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := componentpack.BuildPlugin(&buf, files, nil); err != nil {
		t.Fatal(err)
	}
	p, err := componentpack.UnpackPlugin(bytes.NewReader(buf.Bytes()), filepath.Join(t.TempDir(), "x"), componentpack.PluginExpect{})
	if err != nil {
		t.Fatal(err)
	}
	return p.FilesJSON
}

// signed builds the package of files with developer.sig by dev and store.sig by store
// over signedFiles' files.json (the same files unless a test tampers after signing).
func (k trustKit) signed(t *testing.T, files, signedFiles map[string][]byte, dev, store *pluginsig.Signer, level string) []byte {
	t.Helper()
	fj := filesJSONOf(t, signedFiles)
	sigs := map[string][]byte{}
	if dev != nil {
		b, err := pluginsig.SignDeveloper(*dev, testID, "1.0.0", fj)
		if err != nil {
			t.Fatal(err)
		}
		sigs[componentpack.PluginDeveloperSig] = b
	}
	if store != nil {
		sum, _ := pluginsig.FilesSHA256(fj)
		caps, _ := pluginsig.CapabilitiesSHA256(map[string]any{"optional": []string{"tags.write"}, "required": []string{}})
		b, err := pluginsig.SignStore(*store, pluginsig.StoreSigned{PluginID: testID, Version: "1.0.0", FilesSHA256: sum,
			DeveloperKeyID: k.dev1.ID, ReviewLevel: level, ReviewedAt: pluginsig.FormatTime(k.now.Add(-time.Hour)), CapabilitiesSHA256: caps})
		if err != nil {
			t.Fatal(err)
		}
		sigs[componentpack.PluginStoreSig] = b
	}
	// written by hand, so the files can differ from the signed files.json
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	add := func(name string, b []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(b)
	}
	add(componentpack.PluginManifestName, files[componentpack.PluginManifestName])
	add(componentpack.PluginFilesName, fj)
	for _, n := range []string{componentpack.PluginDeveloperSig, componentpack.PluginStoreSig} {
		if b, ok := sigs[n]; ok {
			add(n, b)
		}
	}
	for _, n := range []string{"i18n/ar.json", "i18n/en.json", "plugin.wasm"} {
		add(n, files[n])
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func (k trustKit) files(t *testing.T, storeKeys []byte, st pluginsig.State) *Files {
	t.Helper()
	f, _ := newFiles(t)
	stateDir := filepath.Dir(f.Layout.Root)
	if storeKeys != nil {
		if err := os.MkdirAll(ListsDir(stateDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ListsDir(stateDir), "store-keys.json"), storeKeys, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := pluginsig.SaveState(pluginsig.TrustFile(stateDir), st); err != nil {
		t.Fatal(err)
	}
	f.Trust = &Trust{Root: k.rootOf(t), StateDir: stateDir}
	return f
}

func TestInstallChecksTheSignatures(t *testing.T) {
	k := newKit()
	good := pluginFiles("1.0.0")
	tampered := pluginFiles("1.0.0")
	tampered["plugin.wasm"] = []byte("\x00asm-not-reallY")
	v3 := k.storeKeys(t, 3, k.store1)
	cases := []struct {
		name       string
		pkg        []byte
		storeKeys  []byte
		state      pluginsig.State
		level      string
		code, step string
	}{
		{name: "reviewed", pkg: k.signed(t, good, good, &k.dev1, &k.store1, "reviewed"), storeKeys: v3, level: "reviewed"},
		{name: "automated", pkg: k.signed(t, good, good, &k.dev1, &k.store1, "automated"), storeKeys: v3, level: "automated"},
		{name: "developer only", pkg: k.signed(t, good, good, &k.dev1, nil, ""), level: "developer"},
		{name: "unsigned", pkg: k.signed(t, good, good, nil, nil, ""), level: "none"},
		{name: "a byte of plugin.wasm changed after signing", pkg: k.signed(t, tampered, good, &k.dev1, &k.store1, "reviewed"),
			storeKeys: v3, code: pluginsig.CodeSignatureInvalid, step: pluginsig.StepDigest},
		{name: "files.json made again over a changed file", pkg: func() []byte {
			var buf bytes.Buffer
			sigs := map[string][]byte{}
			fj := filesJSONOf(t, good)
			sigs[componentpack.PluginDeveloperSig], _ = pluginsig.SignDeveloper(k.dev1, testID, "1.0.0", fj)
			if _, err := componentpack.BuildPlugin(&buf, tampered, sigs); err != nil {
				t.Fatal(err)
			}
			return buf.Bytes()
		}(), code: pluginsig.CodeSignatureInvalid, step: pluginsig.StepDeveloperSig},
		{name: "store.sig on another developer's package", pkg: k.signed(t, good, good, &k.dev2, &k.store1, "reviewed"),
			storeKeys: v3, code: pluginsig.CodeDeveloperKeyMismatch, step: pluginsig.StepDeveloperSig},
		{name: "store-signed with no store-keys.json", pkg: k.signed(t, good, good, &k.dev1, &k.store1, "reviewed"),
			code: pluginsig.CodeSignatureInvalid, step: pluginsig.StepStoreKeys},
		{name: "a revoked store key (rotated out)", pkg: k.signed(t, good, good, &k.dev1, &k.store1, "reviewed"),
			storeKeys: k.storeKeys(t, 4, k.store2), code: pluginsig.CodeSignatureInvalid, step: pluginsig.StepStoreSig},
		{name: "a stale store-keys.json (older than the newest seen)", pkg: k.signed(t, good, good, &k.dev1, &k.store1, "reviewed"),
			storeKeys: v3, state: pluginsig.State{StoreKeysVersion: 4}, code: pluginsig.CodeSignatureInvalid, step: pluginsig.StepStoreKeys},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := k.files(t, c.storeKeys, c.state)
			in, err := f.Install(context.Background(), bytes.NewReader(c.pkg))
			if c.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				if in.ReviewLevel != c.level {
					t.Fatalf("level %s, want %s", in.ReviewLevel, c.level)
				}
				if c.level != "none" && in.DeveloperKeyID != k.dev1.ID {
					t.Fatalf("developer key %s", in.DeveloperKeyID)
				}
				return
			}
			ref, ok := err.(*Refusal)
			if !ok || ref.Code != c.code || ref.Step != c.step {
				t.Fatalf("want %s at %s, got %v", c.code, c.step, err)
			}
			if entries, _ := os.ReadDir(f.Layout.Root); len(entries) != 0 {
				t.Fatalf("a refused install left %d entries", len(entries))
			}
		})
	}
}

// Story 54.17 §3: a store `disable` (plugin_installs.revoked) is the runner's revoked gate,
// before any other.
func TestARevokedInstallIsTheRevokedGate(t *testing.T) {
	m := []byte(`{"maktaba":">=0.1.0","plugin_api":"1.0","capabilities":{"required":[]}}`)
	st := &state{installs: map[string]Install{
		"dev.example.bad": {ID: "dev.example.bad", Manifest: m, ReviewLevel: "reviewed", Revoked: true},
		"dev.example.ok":  {ID: "dev.example.ok", Manifest: m, ReviewLevel: "reviewed"},
	}, grants: map[string][]string{}}
	if r, _ := st.gate("dev.example.bad", "0.7.0"); r != "revoked" {
		t.Fatalf("revoked: %q", r)
	}
	if r, _ := st.gate("dev.example.ok", "0.7.0"); r != "" {
		t.Fatalf("ok: %q", r)
	}
}
