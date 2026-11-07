package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// folderHome serves an installed folder's files as the home's machine route does.
type folderHome struct {
	dir    string
	tamper map[string][]byte
}

func (h *folderHome) Fetch(_ context.Context, path string) (*http.Response, error) {
	i := strings.LastIndex(path, "/files/")
	parts := strings.SplitN(path[i+len("/files/"):], "/", 3)
	b, ok := h.tamper[parts[2]]
	if !ok {
		var err error
		if b, err = os.ReadFile(filepath.Join(h.dir, filepath.FromSlash(parts[2]))); err != nil {
			return &http.Response{StatusCode: 404, Status: "404", Body: io.NopCloser(strings.NewReader(""))}, nil
		}
	}
	return &http.Response{StatusCode: 200, Status: "200", Body: io.NopCloser(bytes.NewReader(b))}, nil
}

// 54.42 §2 T02: a machine places a verified copy of the home's package and points current
// at it; a files.json the home did not install, or a file that differs from it, is
// refused as untrusted and nothing is placed.
func TestAMachineFetchesAVerifiedCopy(t *testing.T) {
	ctx := context.Background()
	home := &Files{Layout: packs.Layout{Root: filepath.Join(t.TempDir(), "home")}}
	in, err := home.Install(ctx, bytes.NewReader(pkg(t, testID, "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	row := Install{ID: in.PluginID, Version: in.Version, Digest: in.Digest, Path: in.Path, ReviewLevel: in.ReviewLevel}
	machine := &Files{Layout: packs.Layout{Root: filepath.Join(t.TempDir(), "machine")}}
	if ref := machine.FetchFromHome(ctx, &folderHome{dir: in.Path}, row); ref != nil {
		t.Fatal(ref)
	}
	if got := machine.Layout.Current(testID); got != filepath.Base(in.Path) {
		t.Fatalf("current %q", got)
	}
	b, _ := os.ReadFile(filepath.Join(machine.Layout.Dir(testID), filepath.Base(in.Path), "plugin.wasm"))
	if string(b) != "\x00asm-not-really" {
		t.Fatalf("copy %q", b)
	}
	for name, h := range map[string]*folderHome{
		"tampered file":      {dir: in.Path, tamper: map[string][]byte{"plugin.wasm": []byte("\x00asm-evil")}},
		"another files.json": {dir: in.Path, tamper: map[string][]byte{"files.json": []byte("[]")}},
	} {
		other := &Files{Layout: packs.Layout{Root: filepath.Join(t.TempDir(), "m")}}
		ref := other.FetchFromHome(ctx, h, row)
		if ref == nil || ref.Code != components.ReasonPluginUntrusted {
			t.Errorf("%s: %v", name, ref)
		}
		if f := other.Layout.Folders(testID); len(f) != 0 {
			t.Errorf("%s: placed %v", name, f)
		}
	}
}

// 54.42 §1: a joined machine runs only the plugins its placement fits, and none whose
// copy did not verify; the home's own runner gates none of them for placement.
func TestAMachineGatesByPlacement(t *testing.T) {
	st := &state{installs: map[string]Install{}, grants: map[string][]string{}, keepOff: map[string]bool{}, allowUnreviewed: true}
	for id, placement := range map[string]string{"dev.example.home": "", "dev.example.any": "any", "dev.example.gpu": "gpu"} {
		st.installs[id] = Install{ID: id, Version: "1.0.0", Manifest: []byte(`{"maktaba":">=0.1.0","plugin_api":"1.0","placement":"` + placement + `"}`)}
	}
	machine := &Runner{Home: &folderHome{}, st: st}
	for id, want := range map[string]string{"dev.example.home": components.ReasonPluginHomeOnly,
		"dev.example.any": "", "dev.example.gpu": components.ReasonPluginNoGPU} {
		if got := machine.Gate(components.PluginPrefix + id); got != want {
			t.Errorf("%s on a machine: %q, want %q", id, got, want)
		}
	}
	machine.GPU = true
	if got := machine.Gate(components.PluginPrefix + "dev.example.gpu"); got != "" {
		t.Errorf("gpu on a GPU machine: %q", got)
	}
	machine.failed = map[string]*Refusal{"dev.example.any": refuse(components.ReasonPluginUntrusted, components.ReasonPluginUntrusted, "x")}
	if got := machine.Gate(components.PluginPrefix + "dev.example.any"); got != components.ReasonPluginUntrusted {
		t.Errorf("untrusted copy: %q", got)
	}
	home := &Runner{st: st}
	if got := home.Gate(components.PluginPrefix + "dev.example.home"); got != "" {
		t.Errorf("the home gates its own: %q", got)
	}
}

// mapHome is a hostile or tampered home: it serves whatever bytes each name has, and
// records what the machine asked for.
type mapHome struct {
	files map[string][]byte
	asked []string
}

func (h *mapHome) Fetch(_ context.Context, path string) (*http.Response, error) {
	i := strings.LastIndex(path, "/files/")
	name := strings.SplitN(path[i+len("/files/"):], "/", 3)[2]
	if u, err := url.PathUnescape(name); err == nil {
		name = u
	}
	h.asked = append(h.asked, name)
	b, ok := h.files[name]
	if !ok {
		return &http.Response{StatusCode: 404, Status: "404", Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return &http.Response{StatusCode: 200, Status: "200", Body: io.NopCloser(bytes.NewReader(b))}, nil
}

// noWrites fails when the machine's layout, or the folder above it, holds anything.
func noWrites(t *testing.T, f *Files, what string) {
	t.Helper()
	parent := filepath.Dir(f.Layout.Root)
	_ = filepath.WalkDir(parent, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("%s: wrote %s", what, p)
		}
		return nil
	})
}

func entry(name string, b []byte) componentpack.PluginFile {
	return componentpack.PluginFile{Path: name, Size: int64(len(b)), SHA256: hexSum(b)}
}

// CodeQL go/path-injection (#166): a files.json from a tampered home that names a path
// outside the package (a "..", an absolute path, a backslash, a colon, a NUL, a name
// twice, one of the package's own names) is refused before the machine writes or
// fetches anything else.
func TestAHostileFilesListWritesNothing(t *testing.T) {
	ctx := context.Background()
	man := []byte(manifest(testID, "1.0.0"))
	for _, name := range []string{"../evil", "a/../../evil", "/etc/evil", `a\b`, "C:/evil", "a\x00b",
		"plugin.wasm", componentpack.PluginFilesName, componentpack.PluginStoreSig, "./a", "a//b"} {
		list := []componentpack.PluginFile{entry(componentpack.PluginManifestName, man), entry(name, []byte("x"))}
		if name == "plugin.wasm" {
			list = append(list, entry(name, []byte("x"))) // listed twice
		}
		fj, _ := json.Marshal(list)
		h := &mapHome{files: map[string][]byte{componentpack.PluginFilesName: fj, componentpack.PluginManifestName: man, name: []byte("x")}}
		m := &Files{Layout: packs.Layout{Root: filepath.Join(t.TempDir(), "plugins")}}
		ref := m.FetchFromHome(ctx, h, Install{ID: testID, Version: "1.0.0", Digest: hexSum(fj), Path: "/home/" + testID + "/1.0.0-ab", ReviewLevel: pluginsig.LevelNone})
		if ref == nil || ref.Code != components.ReasonPluginUntrusted {
			t.Errorf("%q: %v", name, ref)
		}
		if len(h.asked) != 1 {
			t.Errorf("%q: fetched %v after files.json", name, h.asked[1:])
		}
		noWrites(t, m, name)
	}
}

// The signature over files.json is checked before anything is written: a developer.sig
// over another files.json makes no write, and fetches nothing but the signatures and the
// manifest; a file whose sha256 is not files.json's is refused and nothing is placed.
func TestABadSignatureOrDigestWritesNothing(t *testing.T) {
	ctx := context.Background()
	k := newKit()
	good := pluginFiles("1.0.0")
	fj := filesJSONOf(t, good)
	other := pluginFiles("1.0.0")
	other["plugin.wasm"] = []byte("\x00asm-other")
	sig, err := pluginsig.SignDeveloper(k.dev1, testID, "1.0.0", filesJSONOf(t, other))
	if err != nil {
		t.Fatal(err)
	}
	served := map[string][]byte{componentpack.PluginFilesName: fj, componentpack.PluginDeveloperSig: sig}
	for n, b := range good {
		served[n] = b
	}
	row := Install{ID: testID, Version: "1.0.0", Digest: hexSum(fj), Path: "/home/" + testID + "/1.0.0-ab", ReviewLevel: pluginsig.LevelDeveloper}
	h := &mapHome{files: served}
	m := k.files(t, nil, pluginsig.State{})
	if ref := m.FetchFromHome(ctx, h, row); ref == nil || ref.Code != components.ReasonPluginUntrusted {
		t.Fatalf("a bad signature: %v", ref)
	}
	for _, n := range h.asked {
		if n == "plugin.wasm" || strings.HasPrefix(n, "i18n/") {
			t.Errorf("fetched %s before the signature verified", n)
		}
	}
	if f := m.Layout.Folders(testID); len(f) != 0 {
		t.Errorf("placed %v", f)
	}
	if entries, _ := os.ReadDir(m.Layout.Root); len(entries) != 0 {
		t.Errorf("staged %v", entries)
	}
	// the good signature verifies; a payload file whose bytes are not files.json's is refused
	served[componentpack.PluginDeveloperSig], _ = pluginsig.SignDeveloper(k.dev1, testID, "1.0.0", fj)
	served["plugin.wasm"] = []byte("\x00asm-evil")
	m = k.files(t, nil, pluginsig.State{})
	if ref := m.FetchFromHome(ctx, &mapHome{files: served}, row); ref == nil || ref.Code != components.ReasonPluginUntrusted {
		t.Fatalf("a mismatched sha: %v", ref)
	}
	if entries, _ := os.ReadDir(m.Layout.Root); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
	served["plugin.wasm"] = good["plugin.wasm"]
	m = k.files(t, nil, pluginsig.State{})
	if ref := m.FetchFromHome(ctx, &mapHome{files: served}, row); ref != nil {
		t.Fatalf("the signed package: %v", ref)
	}
}

func TestContained(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../x", "a/../../x", "..", "."} {
		if _, err := contained(dir, name); err == nil {
			t.Errorf("%q passed", name)
		}
	}
	if p, err := contained(dir, "a/b.json"); err != nil || p != filepath.Join(dir, "a", "b.json") {
		t.Errorf("a/b.json: %q %v", p, err)
	}
}

// An unsigned package is placed only for an unreviewed install (the admin's opt-in, which
// the gate holds on every machine); one the home installed signed, or from the store,
// that arrives with its signatures stripped is refused, and nothing is written.
func TestStrippedSignaturesAreRefused(t *testing.T) {
	ctx := context.Background()
	k := newKit()
	good := pluginFiles("1.0.0")
	fj := filesJSONOf(t, good)
	unsigned := map[string][]byte{componentpack.PluginFilesName: fj}
	for n, b := range good {
		unsigned[n] = b
	}
	row := func(level string) Install {
		return Install{ID: testID, Version: "1.0.0", Digest: hexSum(fj), Path: "/home/" + testID + "/1.0.0-ab", ReviewLevel: level}
	}
	for _, level := range []string{pluginsig.LevelNone, "development"} {
		m := k.files(t, nil, pluginsig.State{})
		if ref := m.FetchFromHome(ctx, &mapHome{files: unsigned}, row(level)); ref != nil {
			t.Errorf("unsigned, installed %s: %v", level, ref)
		}
	}
	for _, level := range []string{pluginsig.LevelDeveloper, pluginsig.LevelAutomated, pluginsig.LevelReviewed, "store", ""} {
		m := k.files(t, nil, pluginsig.State{})
		h := &mapHome{files: unsigned}
		ref := m.FetchFromHome(ctx, h, row(level))
		if ref == nil || ref.Code != components.ReasonPluginUntrusted {
			t.Errorf("unsigned, installed %q: %v", level, ref)
		}
		if entries, _ := os.ReadDir(m.Layout.Root); len(entries) != 0 {
			t.Errorf("unsigned, installed %q: wrote %v", level, entries)
		}
		for _, n := range h.asked {
			if n == "plugin.wasm" || strings.HasPrefix(n, "i18n/") {
				t.Errorf("unsigned, installed %q: fetched %s", level, n)
			}
		}
	}
	// a store install with store.sig stripped proves developer only: refused
	v3 := k.storeKeys(t, 3, k.store1)
	devSig, err := pluginsig.SignDeveloper(k.dev1, testID, "1.0.0", fj)
	if err != nil {
		t.Fatal(err)
	}
	stripped := map[string][]byte{componentpack.PluginDeveloperSig: devSig}
	for n, b := range unsigned {
		stripped[n] = b
	}
	m := k.files(t, v3, pluginsig.State{})
	if ref := m.FetchFromHome(ctx, &mapHome{files: stripped}, row(pluginsig.LevelReviewed)); ref == nil || ref.Code != components.ReasonPluginUntrusted {
		t.Errorf("store.sig stripped: %v", ref)
	}
	if entries, _ := os.ReadDir(m.Layout.Root); len(entries) != 0 {
		t.Errorf("store.sig stripped: wrote %v", entries)
	}
	// the same package as the developer-signed install it is: placed
	m = k.files(t, v3, pluginsig.State{})
	if ref := m.FetchFromHome(ctx, &mapHome{files: stripped}, row(pluginsig.LevelDeveloper)); ref != nil {
		t.Errorf("developer-signed: %v", ref)
	}
}
