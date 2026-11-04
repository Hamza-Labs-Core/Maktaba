package packs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 51.17: the release's offline file for the Linux maktaba-full package is made from
// a folder of the release's files (`maktaba-server release-offline`: FolderSource as the
// network source, the packs under packs/), and a home with no network imports it.
func TestOfflineFromAReleaseFolder(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for n, b := range f.rel.Files {
		p := filepath.Join(dir, n)
		if strings.HasSuffix(n, ".tar.zst") {
			p = filepath.Join(dir, "packs", n)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.m.Net = &FolderSource{Dir: dir}
	var buf bytes.Buffer
	items, err := f.m.MakeOffline(context.Background(), &buf, OfflineRequest{Components: []string{"runtime.ml"}})
	if err != nil {
		t.Fatalf("%v\n%s", err, f.log.String())
	}
	if strings.Join(items, ",") != "runtime.ml" {
		t.Fatalf("items %v", items)
	}
	if len(f.reqs("")) != 0 {
		t.Fatal("the release folder's file used the network")
	}
	g := newFixture(t, fixtureOpts{})
	g.m.Net, g.m.Trusted = nil, f.m.Trusted
	ids, err := g.m.ImportFrom(context.Background(), bytes.NewReader(buf.Bytes()))
	if err != nil || strings.Join(ids, ",") != "runtime.ml" || !g.m.Installed("runtime.ml") {
		t.Fatalf("imported %v: %v\n%s", ids, err, g.log.String())
	}
	// a folder that lacks the pack: the file is not made
	if err := os.RemoveAll(filepath.Join(dir, "packs")); err != nil {
		t.Fatal(err)
	}
	h := newFixture(t, fixtureOpts{})
	h.m.Net = &FolderSource{Dir: dir}
	if _, err := h.m.MakeOffline(context.Background(), &bytes.Buffer{}, OfflineRequest{Components: []string{"runtime.ml"}}); err == nil {
		t.Fatal("an offline file was made without its pack")
	}
}
