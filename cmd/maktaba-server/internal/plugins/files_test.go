package plugins

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

const testID = "dev.example.hello"

func manifest(id, version string) string {
	return `schema = 1
id = "` + id + `"
version = "` + version + `"
plugin_api = "1.0"
maktaba = ">=0.1.0"
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[capabilities]
optional = ["tags.write"]

[[stage]]
name = "hello"
subject = "video"
export = "run-hello"
version = 1
`
}

// pkg is a .mkplugin of id at version.
func pkg(t *testing.T, id, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	_, err := componentpack.BuildPlugin(&buf, map[string][]byte{
		"manifest.toml": []byte(manifest(id, version)),
		"i18n/en.json":  []byte(`{"plugin.name":"Hello","plugin.summary":"Says hello."}`),
		"i18n/ar.json":  []byte(`{"plugin.name":"مرحبا","plugin.summary":"يقول مرحبا."}`),
		"plugin.wasm":   []byte("\x00asm-not-really"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newFiles(t *testing.T) (*Files, *[]string) {
	t.Helper()
	var preloaded []string
	f := &Files{Layout: packs.Layout{Root: filepath.Join(t.TempDir(), "plugins")},
		Preload: func(_ context.Context, id, version, dir string) *Refusal {
			preloaded = append(preloaded, id+"@"+version+"@"+filepath.Base(dir))
			return nil
		}}
	return f, &preloaded
}

// 54.38 T01: a package is unpacked, validated, preloaded and put in its folder; current
// names it; the same package again writes nothing.
func TestInstallPutsAPackageInPlace(t *testing.T) {
	f, preloaded := newFiles(t)
	in, err := f.Install(context.Background(), bytes.NewReader(pkg(t, testID, "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	if in.PluginID != testID || in.Version != "1.0.0" || len(in.Digest) != 64 || in.Already {
		t.Fatalf("installed %+v", in)
	}
	if want := "1.0.0_" + in.Digest[:12]; in.Folder != want || f.Layout.Current(testID) != want {
		t.Fatalf("folder %s, current %s", in.Folder, f.Layout.Current(testID))
	}
	if _, err := os.Stat(filepath.Join(in.Path, "plugin.wasm")); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(in.Manifest, &m); err != nil || m["id"] != testID {
		t.Fatalf("manifest %s", in.Manifest)
	}
	if len(*preloaded) != 1 {
		t.Fatalf("preloads %v", *preloaded)
	}
	again, err := f.Install(context.Background(), bytes.NewReader(pkg(t, testID, "1.0.0")))
	if err != nil || !again.Already || len(*preloaded) != 1 {
		t.Fatalf("again %+v %v (preloads %v)", again, err, *preloaded)
	}
	// a new version goes beside it; current stays (the switch is plugin_installs')
	v2, err := f.Install(context.Background(), bytes.NewReader(pkg(t, testID, "1.1.0")))
	if err != nil {
		t.Fatal(err)
	}
	if f.Layout.Current(testID) != in.Folder || len(f.Layout.Folders(testID)) != 2 {
		t.Fatalf("after 1.1.0: current %s, folders %v", f.Layout.Current(testID), f.Layout.Folders(testID))
	}
	if removed, err := f.Discard(context.Background(), testID, v2.Folder); err != nil || !removed {
		t.Fatalf("discard %v %v", removed, err)
	}
	if removed, _ := f.Discard(context.Background(), testID, in.Folder); removed {
		t.Fatal("the current folder was discarded")
	}
	if entries, _ := os.ReadDir(f.Layout.Root); hasStaging(entries) {
		t.Fatal("a staging folder was left")
	}
}

func hasStaging(entries []os.DirEntry) bool {
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), packs.StagingPrefix) {
			return true
		}
	}
	return false
}

// hostile is a .mkplugin whose tar holds one more entry after the head.
func hostile(t *testing.T, hdr *tar.Header, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	man := []byte(manifest(testID, "1.0.0"))
	files, _ := json.Marshal([]componentpack.PluginFile{})
	for _, f := range []struct {
		name string
		b    []byte
	}{{"manifest.toml", man}, {"files.json", files}} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(f.b)
	}
	_ = tw.WriteHeader(hdr)
	if len(body) > 0 {
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// 54.38 T02: hostile packages are refused, unpack_refused, and nothing stays; a manifest
// the validator refuses is refused with its code.
func TestHostilePackagesLeaveNothing(t *testing.T) {
	cases := map[string][]byte{
		"dotdot":   hostile(t, &tar.Header{Name: "../x", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, []byte("x")),
		"absolute": hostile(t, &tar.Header{Name: "/etc/x", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}, []byte("x")),
		"hardlink": hostile(t, &tar.Header{Name: "l", Linkname: "manifest.toml", Typeflag: tar.TypeLink}, nil),
		"device":   hostile(t, &tar.Header{Name: "d", Typeflag: tar.TypeChar, Mode: 0o644}, nil),
	}
	for name, b := range cases {
		f, _ := newFiles(t)
		_, err := f.Install(context.Background(), bytes.NewReader(b))
		var ref *Refusal
		if !errors.As(err, &ref) || ref.Reason != ReasonUnpackRefused {
			t.Fatalf("%s: %v", name, err)
		}
		entries, _ := os.ReadDir(f.Layout.Root)
		if len(entries) != 0 {
			t.Fatalf("%s: left %v", name, entries)
		}
	}
	f, _ := newFiles(t)
	_, err := f.Install(context.Background(), bytes.NewReader(pkg(t, "Not.An.Id", "1.0.0")))
	var ref *Refusal
	if !errors.As(err, &ref) || !strings.HasPrefix(ref.Code, "manifest_") {
		t.Fatalf("a bad manifest: %v", err)
	}
}

// A package the host refuses at its preload (a forbidden import) is removed.
func TestPreloadRefusalRemovesTheFolder(t *testing.T) {
	f, _ := newFiles(t)
	f.Preload = func(context.Context, string, string, string) *Refusal {
		return refuse("plugin_forbidden_import", ReasonUnpackRefused, "wasi:sockets")
	}
	_, err := f.Install(context.Background(), bytes.NewReader(pkg(t, testID, "1.0.0")))
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Code != "plugin_forbidden_import" {
		t.Fatalf("%v", err)
	}
	if got := f.Layout.Folders(testID); len(got) != 0 || f.Layout.Current(testID) != "" {
		t.Fatalf("left %v", got)
	}
}

// 54.38 T03, T05: a plugin's pointer follows its plugin_installs version even when it
// equals the server's; packs' Recover(version) keeps its answers.
func TestRecoverWithPerPluginVersion(t *testing.T) {
	root := t.TempDir()
	lay := packs.Layout{Root: root}
	for _, d := range []string{"0.9.0_aaaaaaaaaaaa", "0.8.0_bbbbbbbbbbbb", ".staging-x"} {
		if err := os.MkdirAll(filepath.Join(lay.Dir(testID), d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = lay.SetCurrent(testID, "0.9.0_aaaaaaaaaaaa")
	lay.RecoverWith(func(string) string { return "0.8.0" })
	if got := lay.Current(testID); got != "0.8.0_bbbbbbbbbbbb" {
		t.Fatalf("current %s, want the plugin_installs version's", got)
	}
	if _, err := os.Stat(filepath.Join(lay.Dir(testID), ".staging-x")); !os.IsNotExist(err) {
		t.Fatal("the staging folder stayed")
	}
	// packs: the server's version, as before
	_ = lay.SetCurrent(testID, "0.8.0_bbbbbbbbbbbb")
	lay.Recover("0.9.0")
	if got := lay.Current(testID); got != "0.9.0_aaaaaaaaaaaa" {
		t.Fatalf("Recover(version) answered %s", got)
	}
}

// 54.11 T01: a settings schema with a secret field is refused at install.
func TestASecretSettingIsRefused(t *testing.T) {
	f, _ := newFiles(t)
	var buf bytes.Buffer
	_, err := componentpack.BuildPlugin(&buf, map[string][]byte{
		"manifest.toml":           []byte(manifest(testID, "1.0.0") + "\n[settings]\nschema = \"ui/settings.schema.json\"\n"),
		"i18n/en.json":            []byte(`{"plugin.name":"Hello","plugin.summary":"Says hello.","s.k":"Key"}`),
		"i18n/ar.json":            []byte(`{"plugin.name":"مرحبا","plugin.summary":"يقول مرحبا.","s.k":"مفتاح"}`),
		"ui/settings.schema.json": []byte(`{"type":"object","properties":{"api_key":{"type":"string","default":"","x-maktaba-title":"s.k"}}}`),
		"plugin.wasm":             []byte("\x00asm-not-really"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Install(context.Background(), bytes.NewReader(buf.Bytes()))
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Code != "settings_secret_forbidden" {
		t.Fatalf("%v", err)
	}
}
