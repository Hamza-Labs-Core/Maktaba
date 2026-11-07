package plugins

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
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
	row := Install{ID: in.PluginID, Version: in.Version, Digest: in.Digest, Path: in.Path}
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
