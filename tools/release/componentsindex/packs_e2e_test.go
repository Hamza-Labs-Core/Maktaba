//go:build e2etrust

package componentsindex

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
	"github.com/klauspost/compress/zstd"
)

// TestPacksEndToEnd is the components-packs CI job (story 51.15 T12): the packs build.sh
// made for one platform (MAKTABA_PACKS_DIR) go into a release signed by a fake Sigstore
// whose root this e2etrust build trusts through MAKTABA_E2E_TRUSTED_ROOT, as a developer
// build would; the index verifies with the store's trust root, every pack matches it and
// unpacks. A flipped byte, an expired index, a changed pack and a path traversal are
// each refused.
func TestPacksEndToEnd(t *testing.T) {
	dist := os.Getenv("MAKTABA_PACKS_DIR")
	if dist == "" {
		t.Skip("MAKTABA_PACKS_DIR is not set (the components-packs job sets it)")
	}
	version := os.Getenv("MAKTABA_PACKS_VERSION")
	if version == "" {
		version = "0.4.2"
	}
	tag := "v" + version
	pub := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	ss := releaseverifytest.MustNew()
	rootJSON, err := ss.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	rootFile := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootFile, rootJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_E2E_TRUSTED_ROOT", rootFile)
	tm, _, err := (&rv.RootStore{}).TrustedMaterial()
	if err != nil {
		t.Fatal(err)
	}

	// the one platform build.sh built here (MAKTABA_PACKS_PLATFORM, linux-amd64 by default):
	// every pack of it must be there
	platform := os.Getenv("MAKTABA_PACKS_PLATFORM")
	if platform == "" {
		platform = "linux-amd64"
	}
	idx, err := Generate(Options{Catalog: components.Default(), Version: version,
		Commit: strings.Repeat("0a", 20), Published: pub, Dist: dist, Platform: platform})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d packs of %s in %s", len(idx.Packs), platform, dist)
	if len(idx.Packs) == 0 {
		t.Fatal("no packs were built")
	}
	ib, err := Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	digests := map[string]string{}
	for _, p := range idx.Packs {
		for _, f := range p.Files() {
			digests[f.Name] = f.SHA256
		}
	}
	rel, err := ss.NewReleaseWith("Hamza-Labs-Core/Maktaba", tag, "linux/amd64",
		releaseverifytest.ReleaseOptions{ExtraDigests: digests, ComponentsIndex: ib})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := rv.NewIdentity("")
	input := func(index []byte, now time.Time) rv.ComponentsInput {
		return rv.ComponentsInput{Tag: tag, Identity: id, TrustedMaterial: tm,
			Checksums: rel.Files["checksums.txt"], Bundle: rel.Files["checksums.txt.sigstore.json"],
			Manifest: rel.Files["release.json"], Index: index, ServerVersion: version, Now: now,
			Seen: rv.IndexWatermarkFile{Path: rv.IndexWatermarkPath(t.TempDir())}}
	}

	v, err := rv.VerifyComponentsIndex(input(ib, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.Index.Packs {
		if err := rv.CheckPackFiles(p, dist); err != nil {
			t.Fatal(err)
		}
		r, _, closeAll, err := componentpack.Open(dist, p.Name)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "x")
		m, err := componentpack.Unpack(r, dest, componentpack.Expect{ID: p.ID, Version: version, Platform: p.Platform,
			FilesSHA256: p.FilesSHA256, UnpackedBytes: p.UnpackedBytes})
		_ = closeAll()
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		t.Logf("%s: %d bytes, %d unpacked: verified and unpacked", p.Name, p.Size, m.UnpackedBytes)
		_ = os.RemoveAll(dest)
	}

	// a flipped byte of the index
	bad := append([]byte(nil), ib...)
	bad[len(bad)/3] ^= 1
	if _, err := rv.VerifyComponentsIndex(input(bad, time.Now())); rv.IndexReason(err) != "verify_failed" {
		t.Fatalf("flipped byte: %v", err)
	}
	// past expires
	if _, err := rv.VerifyComponentsIndex(input(ib, idx.Expires.Add(time.Second))); rv.IndexReason(err) != "index_expired" {
		t.Fatalf("expired: %v", err)
	}
	// a pack changed after signing: same name, other bytes
	p := v.Index.Packs[0]
	other := t.TempDir()
	for _, f := range p.Files() {
		if err := os.WriteFile(filepath.Join(other, f.Name), []byte("not the pack"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := rv.CheckPackFiles(p, other); !errors.Is(err, rv.ErrChecksum) {
		t.Fatalf("changed pack: %v", err)
	}
	// a pack with a path that climbs out
	if _, err := componentpack.Unpack(bytes.NewReader(traversal(t)), filepath.Join(t.TempDir(), "x"), componentpack.Expect{}); !errors.Is(err, componentpack.ErrInvalid) {
		t.Fatalf("path traversal: %v", err)
	}
}

func traversal(t *testing.T) []byte {
	t.Helper()
	body := []byte("x")
	sums := componentpack.FormatSums([]componentpack.Entry{{Kind: 'f', Mode: 0o644, Size: 1,
		SHA256: "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881", Path: "a"}})
	man := []byte(`{"schema":1,"id":"tool.x","version":"0.4.2","platform":"linux-amd64","files_sha256":"` + sha(sums) + `","unpacked_bytes":1}`)
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range []struct {
		name string
		b    []byte
	}{{componentpack.ManifestName, man}, {componentpack.SumsName, sums}, {"../../a", body}} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e.b))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(e.b)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
