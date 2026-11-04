package componentpack

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

var epoch = time.Unix(1762160000, 0)

// tree writes a small pack tree: two files, a nested one and a link.
func tree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "wheels", "sub"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "constraints.txt"), []byte("anyio==4.4.0 --hash=sha256:ab\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "wheels", "a-1.0-py3-none-any.whl"), bytes.Repeat([]byte("wheel"), 4000), 0o644))
	must(os.WriteFile(filepath.Join(dir, "wheels", "sub", "tool"), []byte("#!/bin/sh\n"), 0o755))
	must(os.Symlink("a-1.0-py3-none-any.whl", filepath.Join(dir, "wheels", "latest.whl")))
	return dir
}

func build(t *testing.T, dir, out string, part int64) *Built {
	t.Helper()
	b, err := Build(Options{Dir: dir, ID: "runtime.ml", Version: "0.4.2", Platform: "linux-amd64", ModTime: epoch, Out: out, PartSize: part})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// T09 (Go half): the same tree and time build the same bytes, and unpack to the tree.
func TestBuildReproducibleAndUnpacks(t *testing.T) {
	dir := tree(t)
	out1, out2 := t.TempDir(), t.TempDir()
	b1, b2 := build(t, dir, out1, 0), build(t, dir, out2, 0)
	if b1.Name != "runtime.ml-0.4.2-linux-amd64.tar.zst" {
		t.Fatalf("name %s", b1.Name)
	}
	p1, _ := os.ReadFile(filepath.Join(out1, b1.Name))
	p2, _ := os.ReadFile(filepath.Join(out2, b2.Name))
	if !bytes.Equal(p1, p2) || b1.SHA256 != b2.SHA256 || b1.Size != int64(len(p1)) {
		t.Fatal("two builds of one tree differ")
	}
	if sha256Hex(p1) != b1.SHA256 {
		t.Fatal("Built.SHA256 is not the file's")
	}
	man, entries, err := ReadHead(bytes.NewReader(p1))
	if err != nil {
		t.Fatal(err)
	}
	if man.ID != "runtime.ml" || man.FilesSHA256 != b1.Manifest.FilesSHA256 || len(entries) != 4 {
		t.Fatalf("head %+v, %d entries", man, len(entries))
	}
	dest := filepath.Join(t.TempDir(), "x")
	if _, err := Unpack(bytes.NewReader(p1), dest, Expect{ID: "runtime.ml", Version: "0.4.2", Platform: "linux-amd64", FilesSHA256: man.FilesSHA256}); err != nil {
		t.Fatal(err)
	}
	got, total, err := Scan(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(FormatSums(got), FormatSums(entries)) || total != man.UnpackedBytes {
		t.Fatalf("unpacked tree differs:\n%s\nwant\n%s", FormatSums(got), FormatSums(entries))
	}
	if info, _ := os.Stat(filepath.Join(dest, "wheels", "sub", "tool")); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", info.Mode())
	}
}

// A pack over the part size is split; the parts joined are the pack.
func TestBuildSplits(t *testing.T) {
	dir := tree(t)
	whole := build(t, dir, t.TempDir(), 0)
	out := t.TempDir()
	b := build(t, dir, out, 64)
	if len(b.Parts) < 2 {
		t.Fatalf("%d parts", len(b.Parts))
	}
	if b.SHA256 != whole.SHA256 || b.Size != whole.Size {
		t.Fatal("split pack's whole digest differs from the unsplit one")
	}
	if _, err := os.Stat(filepath.Join(out, b.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a split pack left the whole file too")
	}
	r, names, closeAll, err := Open(out, b.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeAll() }()
	if len(names) != len(b.Parts) {
		t.Fatalf("opened %d parts", len(names))
	}
	if _, err := Unpack(r, filepath.Join(t.TempDir(), "x"), Expect{}); err != nil {
		t.Fatal(err)
	}
}

func TestScanRefuses(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("../../etc/passwd", filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Scan(dir); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a link out of the folder: %v", err)
	}
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Scan(dir); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a file named pack.json: %v", err)
	}
}

// craft writes a pack by hand: pack.json and files.sha256 from sums, then entries.
type entry struct {
	hdr  tar.Header
	body []byte
}

func craft(t *testing.T, sums string, entries ...entry) []byte {
	t.Helper()
	var total int64
	if es, err := ParseSums([]byte(sums)); err == nil {
		for _, e := range es {
			total += e.Size
		}
	}
	man, _ := json.Marshal(Manifest{Schema: 1, ID: "tool.x", Version: "0.4.2", Platform: "linux-amd64", FilesSHA256: sha256Hex([]byte(sums)), UnpackedBytes: total})
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	all := append([]entry{
		{tar.Header{Name: ManifestName, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(man))}, man},
		{tar.Header{Name: SumsName, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(sums))}, []byte(sums)},
	}, entries...)
	for _, e := range all {
		h := e.hdr
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func fileLine(path string, body []byte) string {
	return Entry{Kind: 'f', Mode: 0o644, Size: int64(len(body)), SHA256: sha256Hex(body), Path: path}.Line() + "\n"
}

func reg(name string, body []byte) entry {
	return entry{tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}, body}
}

// README §6.4: absolute paths, "..", links that leave the folder, devices, hard links,
// unlisted or changed files are all refused, and nothing is left behind.
func TestUnpackRefuses(t *testing.T) {
	body := []byte("hello")
	ok := fileLine("a.txt", body)
	cases := map[string][]byte{
		"path traversal":   craft(t, ok, reg("../a.txt", body)),
		"traversal listed": craft(t, fileLine("../a.txt", body), reg("../a.txt", body)),
		"absolute path":    craft(t, ok, reg("/tmp/a.txt", body)),
		"link out": craft(t, "l\t../../etc/passwd\tx\n",
			entry{tar.Header{Name: "x", Typeflag: tar.TypeSymlink, Linkname: "../../etc/passwd"}, nil}),
		"absolute link": craft(t, "l\t/etc/passwd\tx\n",
			entry{tar.Header{Name: "x", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, nil}),
		"device": craft(t, ok, reg("a.txt", body),
			entry{tar.Header{Name: "dev", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}, nil}),
		"listed device": craft(t, ok+fileLine("dev", nil),
			reg("a.txt", body), entry{tar.Header{Name: "dev", Typeflag: tar.TypeChar}, nil}),
		"hard link": craft(t, ok+fileLine("b.txt", body), reg("a.txt", body),
			entry{tar.Header{Name: "b.txt", Typeflag: tar.TypeLink, Linkname: "a.txt"}, nil}),
		"unlisted file": craft(t, ok, reg("a.txt", body), reg("b.txt", body)),
		"changed file":  craft(t, ok, reg("a.txt", []byte("HELLO"))),
		"missing file":  craft(t, ok+fileLine("b.txt", body), reg("a.txt", body)),
		"twice":         craft(t, ok, reg("a.txt", body), reg("a.txt", body)),
		"through a link": craft(t, "l\tsub\td\n"+fileLine("d/x", body),
			entry{tar.Header{Name: "d", Typeflag: tar.TypeSymlink, Linkname: "sub"}, nil}, reg("d/x", body)),
		"setuid mode": craft(t, strings.Replace(ok, "0644", "4755", 1), reg("a.txt", body)),
		// story 51.16 T01: the rest of the unsafe entries
		"case clash": craft(t, fileLine("A.txt", body)+ok, reg("A.txt", body), reg("a.txt", body)),
		"case clash in a folder": craft(t, fileLine("D/a.txt", body)+fileLine("d/b.txt", body),
			reg("D/a.txt", body), reg("d/b.txt", body)),
		"windows drive": craft(t, ok, reg(`C:\x`, body)),
		"unc path":      craft(t, ok, reg(`\\host\share\x`, body)),
		"fifo": craft(t, ok+fileLine("p", nil), reg("a.txt", body),
			entry{tar.Header{Name: "p", Typeflag: tar.TypeFifo}, nil}),
	}
	for name, pack := range cases {
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "x")
			_, err := Unpack(bytes.NewReader(pack), dest, Expect{})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("a refused pack left its folder behind")
			}
		})
	}
	// the control: the same pack, well formed, unpacks
	if _, err := Unpack(bytes.NewReader(craft(t, ok, reg("a.txt", body))), filepath.Join(t.TempDir(), "x"), Expect{}); err != nil {
		t.Fatal(err)
	}
}

func TestUnpackChecksTheIndex(t *testing.T) {
	body := []byte("hello")
	pack := craft(t, fileLine("a.txt", body), reg("a.txt", body))
	for name, want := range map[string]Expect{
		"other files.sha256": {FilesSHA256: strings.Repeat("0", 64)},
		"other id":           {ID: "runtime.ml"},
		"other version":      {Version: "0.4.3"},
		"other size":         {UnpackedBytes: 99},
	} {
		if _, err := Unpack(bytes.NewReader(pack), filepath.Join(t.TempDir(), "x"), want); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a pack whose pack.json names other files.sha256 bytes
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	man := []byte(`{"schema":1,"id":"tool.x","version":"0.4.2","platform":"linux-amd64","files_sha256":"` + strings.Repeat("a", 64) + `","unpacked_bytes":0}`)
	_ = tw.WriteHeader(&tar.Header{Name: ManifestName, Typeflag: tar.TypeReg, Size: int64(len(man))})
	_, _ = tw.Write(man)
	_ = tw.WriteHeader(&tar.Header{Name: SumsName, Typeflag: tar.TypeReg})
	_ = tw.Close()
	_ = zw.Close()
	if _, err := Unpack(&buf, filepath.Join(t.TempDir(), "x"), Expect{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("files.sha256 not the one pack.json names: %v", err)
	}
}

func TestUnpackNeedsANewFolder(t *testing.T) {
	body := []byte("hello")
	dest := t.TempDir()
	if _, err := Unpack(bytes.NewReader(craft(t, fileLine("a.txt", body), reg("a.txt", body))), dest, Expect{}); err == nil {
		t.Fatal("unpacked into a folder that exists")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("removed a folder it did not make")
	}
}
