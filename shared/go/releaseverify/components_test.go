package releaseverify_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
	"github.com/sigstore/sigstore-go/pkg/root"
)

// Story 51.15 T01–T07: the signed component index.

const indexTag = "v0.4.2"

func hexOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type indexFixture struct {
	ss    *releaseverifytest.Sigstore
	tm    *root.TrustedRoot
	rel   *releaseverifytest.FakeRelease
	pack  []byte
	index rv.ComponentsIndex
	seen  rv.IndexWatermarkFile
}

var published = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// newIndexFixture is a signed 0.4.2 release with one pack and its index, published at p.
func newIndexFixture(t *testing.T, p time.Time, edit func(*rv.ComponentsIndex)) *indexFixture {
	t.Helper()
	ss, err := releaseverifytest.New()
	if err != nil {
		t.Fatal(err)
	}
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	f := &indexFixture{ss: ss, tm: tm, pack: []byte("a pack's bytes"),
		seen: rv.IndexWatermarkFile{Path: rv.IndexWatermarkPath(t.TempDir())}}
	f.index = rv.ComponentsIndex{
		Schema: 1, Version: "0.4.2", Commit: strings.Repeat("0a", 20), Published: p, Expires: p.Add(rv.IndexLifetime),
		Server: rv.ServerRange("0.4.2"),
		Packs: []rv.IndexPack{{ID: "runtime.ml", Platform: "linux-amd64", Name: "runtime.ml-0.4.2-linux-amd64.tar.zst",
			SHA256: hexOf(f.pack), Size: int64(len(f.pack)), UnpackedBytes: 99, FilesSHA256: strings.Repeat("ab", 32), Requires: []string{}}},
		Models: []rv.IndexModel{{ID: "model.whisper-turbo", Source: "hf:dropbox-dash/faster-whisper-large-v3-turbo@0a363e9161cbc7ed1431c9597a8ceaf0c4f78fcf",
			Platforms: []string{"linux-amd64"}, Files: []rv.IndexFile{{Path: "model.bin", SHA256: strings.Repeat("cd", 32), Size: 1617000000}}}},
	}
	if edit != nil {
		edit(&f.index)
	}
	ib, err := json.MarshalIndent(f.index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f.rel, err = ss.NewReleaseWith(repo, indexTag, platform, releaseverifytest.ReleaseOptions{
		Extra: map[string][]byte{f.index.Packs[0].Name: f.pack}, ComponentsIndex: ib})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *indexFixture) input(now time.Time, server string) rv.ComponentsInput {
	id, _ := rv.NewIdentity(repo)
	r := f.rel
	return rv.ComponentsInput{Tag: r.Tag, Identity: id, TrustedMaterial: f.tm,
		Checksums: r.Files["checksums.txt"], Bundle: r.Files["checksums.txt.sigstore.json"], Manifest: r.Files["release.json"],
		Index: r.Files[r.ComponentsIndexName()], ServerVersion: server, Now: now, Seen: f.seen}
}

func reason(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("verified; want %s", want)
	}
	if got := rv.IndexReason(err); got != want {
		t.Fatalf("reason %s (%v); want %s", got, err, want)
	}
}

// T01: a signed release with an index passes, and every pack matches the index.
func TestComponentsIndexVerifies(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	v, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.2"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Index.Version != "0.4.2" || len(v.Index.Packs) != 1 || v.SHA256 != hexOf(f.rel.Files[f.rel.ComponentsIndexName()]) {
		t.Fatalf("verified %+v", v.Index)
	}
	dir := t.TempDir()
	if err := f.rel.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := rv.CheckPackFiles(v.Index.Packs[0], dir); err != nil {
		t.Fatal(err)
	}
	seen, err := f.seen.Load()
	if err != nil || !seen["0.4.2"].Published.Equal(published) {
		t.Fatalf("watermark %+v, %v", seen, err)
	}
}

// T02: one byte of the index flipped is verify_failed, and nothing is written.
func TestComponentsIndexFlippedByte(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	in := f.input(published.Add(time.Hour), "0.4.2")
	b := append([]byte(nil), in.Index...)
	b[len(b)/2] ^= 1
	in.Index = b
	_, err := rv.VerifyComponentsIndex(in)
	reason(t, err, "verify_failed")
	if !errors.Is(err, rv.ErrChecksum) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(f.seen.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused index wrote the watermark")
	}
}

// release.json's digest and checksums.txt's line must agree with the index.
func TestComponentsIndexReleaseJSONDisagrees(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	f.rel.Manifest["components"] = map[string]any{"index": f.rel.ComponentsIndexName(), "sha256": strings.Repeat("0", 64)}
	if err := f.rel.Rebuild(f.ss, releaseverifytest.GitHubRelease(repo, indexTag)); err != nil {
		t.Fatal(err)
	}
	_, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.2"))
	reason(t, err, "verify_failed")
}

// A pack whose index digest is not checksums.txt's is refused.
func TestComponentsIndexPackNotInChecksums(t *testing.T) {
	f := newIndexFixture(t, published, func(i *rv.ComponentsIndex) { i.Packs[0].SHA256 = strings.Repeat("ee", 32) })
	_, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.2"))
	reason(t, err, "verify_failed")
}

// T03: an index past its expires is index_expired.
func TestComponentsIndexExpired(t *testing.T) {
	now := time.Now()
	f := newIndexFixture(t, now.Add(-2*rv.IndexLifetime), func(i *rv.ComponentsIndex) { i.Expires = now.Add(-24 * time.Hour) })
	_, err := rv.VerifyComponentsIndex(f.input(now, "0.4.2"))
	reason(t, err, "index_expired")
}

// T04: after 0.4.2's index published at T, one published before T, or at T with other
// bytes, is index_replayed.
func TestComponentsIndexReplayed(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	if _, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.2")); err != nil {
		t.Fatal(err)
	}
	older := newIndexFixture(t, published.Add(-time.Second), nil)
	older.seen = f.seen
	_, err := rv.VerifyComponentsIndex(older.input(published.Add(time.Hour), "0.4.2"))
	reason(t, err, "index_replayed")

	other := newIndexFixture(t, published, func(i *rv.ComponentsIndex) { i.Models = nil })
	other.seen = f.seen
	_, err = rv.VerifyComponentsIndex(other.input(published.Add(time.Hour), "0.4.2"))
	reason(t, err, "index_replayed")

	// the same index again passes (a re-check)
	if _, err := rv.VerifyComponentsIndex(f.input(published.Add(2*time.Hour), "0.4.2")); err != nil {
		t.Fatal(err)
	}
}

// T05: a server at 0.4.3 offered 0.4.2's index: index_wrong_version.
func TestComponentsIndexWrongVersion(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	_, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.3"))
	reason(t, err, "index_wrong_version")
}

// T06: a watermark for 0.4.3 does not stop a home rolled back to 0.4.2.
func TestComponentsIndexRolledBack(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	if err := f.seen.Commit("0.4.3", published.Add(30*24*time.Hour), strings.Repeat("11", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.2")); err != nil {
		t.Fatal(err)
	}
	seen, _ := f.seen.Load()
	if len(seen) != 2 {
		t.Fatalf("watermark %+v", seen)
	}
}

// T07: checksums.txt signed by another workflow is verify_failed (the identity).
func TestComponentsIndexOtherWorkflow(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	o := releaseverifytest.GitHubRelease(repo, indexTag)
	o.SAN = strings.Replace(o.SAN, "release.yml", "ci.yml", 1)
	o.BuildConfigURI = o.SAN
	if err := f.rel.Resign(f.ss, o); err != nil {
		t.Fatal(err)
	}
	_, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.2"))
	reason(t, err, "verify_failed")
	if !errors.Is(err, rv.ErrIdentity) {
		t.Fatalf("err %v", err)
	}
}

// A release from before 51.15 has no index: index_missing.
func TestComponentsIndexMissing(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	delete(f.rel.Manifest, "components")
	if err := f.rel.Rebuild(f.ss, releaseverifytest.GitHubRelease(repo, indexTag)); err != nil {
		t.Fatal(err)
	}
	_, err := rv.VerifyComponentsIndex(f.input(published.Add(time.Hour), "0.4.2"))
	reason(t, err, "index_missing")
}

func TestParseComponentsIndexRefuses(t *testing.T) {
	good := func() rv.ComponentsIndex { return newIndexFixture(t, published, nil).index }
	for name, edit := range map[string]func(*rv.ComponentsIndex){
		"schema 2":      func(i *rv.ComponentsIndex) { i.Schema = 2 },
		"range server":  func(i *rv.ComponentsIndex) { i.Server = ">=0.4.2" },
		"branch model":  func(i *rv.ComponentsIndex) { i.Models[0].Source = "hf:dropbox-dash/faster-whisper-large-v3-turbo@main" },
		"model .. path": func(i *rv.ComponentsIndex) { i.Models[0].Files[0].Path = "../model.bin" },
		"pack name":     func(i *rv.ComponentsIndex) { i.Packs[0].Name = "../x.tar.zst" },
		"one part": func(i *rv.ComponentsIndex) {
			i.Packs[0].Parts = []rv.IndexPart{{Name: i.Packs[0].Name + ".part1", SHA256: i.Packs[0].SHA256, Size: 1}}
		},
		"expires before": func(i *rv.ComponentsIndex) { i.Expires = i.Published.Add(-time.Hour) },
	} {
		i := good()
		edit(&i)
		b, _ := json.Marshal(i)
		if _, err := rv.ParseComponentsIndex(b); !errors.Is(err, rv.ErrManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A pack's size is checked before its bytes, then its sha256; a split pack's parts too.
func TestCheckPackFiles(t *testing.T) {
	dir := t.TempDir()
	body := []byte("0123456789")
	p := rv.IndexPack{Name: "tool.x-0.4.2-linux-amd64.tar.zst", SHA256: hexOf(body), Size: 10}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(p.Name, body)
	if err := rv.CheckPackFiles(p, dir); err != nil {
		t.Fatal(err)
	}
	write(p.Name, []byte("0123456789!"))
	if err := rv.CheckPackFiles(p, dir); !errors.Is(err, rv.ErrChecksum) || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("bigger file: %v", err)
	}
	write(p.Name, []byte("012345678X"))
	if err := rv.CheckPackFiles(p, dir); !errors.Is(err, rv.ErrChecksum) || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("changed file: %v", err)
	}
	p.Parts = []rv.IndexPart{{Name: p.Name + ".part1", SHA256: hexOf(body[:6]), Size: 6}, {Name: p.Name + ".part2", SHA256: hexOf(body[6:]), Size: 4}}
	write(p.Parts[0].Name, body[:6])
	write(p.Parts[1].Name, body[6:])
	if err := rv.CheckPackFiles(p, dir); err != nil {
		t.Fatal(err)
	}
	p.SHA256 = strings.Repeat("0", 64)
	if err := rv.CheckPackFiles(p, dir); !errors.Is(err, rv.ErrChecksum) {
		t.Fatalf("parts joined to another digest: %v", err)
	}
}

// release.json's components must be listed in checksums.txt with its digest.
func TestManifestValidatesComponents(t *testing.T) {
	f := newIndexFixture(t, published, nil)
	m, err := rv.ParseManifest(f.rel.Files["release.json"])
	if err != nil {
		t.Fatal(err)
	}
	sums, _ := rv.ParseChecksums(f.rel.Files["checksums.txt"])
	if err := m.Validate(indexTag, sums); err != nil {
		t.Fatal(err)
	}
	m.Components.Index = "components-0.4.3.json"
	if err := m.Validate(indexTag, sums); !errors.Is(err, rv.ErrManifest) {
		t.Fatalf("other index name: %v", err)
	}
}
