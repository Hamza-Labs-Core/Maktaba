package componentpack

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func samplePlugin() map[string][]byte {
	return map[string][]byte{
		"manifest.toml": []byte("schema = 1\n"),
		"plugin.wasm":   []byte("\x00asm component"),
		"i18n/en.json":  []byte(`{"plugin.name":"x"}`),
		"i18n/ar.json":  []byte(`{"plugin.name":"س"}`),
		"LICENSE":       []byte("MIT"),
	}
}

func buildPlugin(t *testing.T, files map[string][]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	sum, err := BuildPlugin(&buf, files, map[string][]byte{PluginDeveloperSig: []byte("sig")})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), sum
}

// rawPlugin writes a package by hand, so a test can put anything in it.
type rawEntry struct {
	hdr  tar.Header
	body []byte
}

func rawPlugin(t *testing.T, list []PluginFile, entries []rawEntry, manifest []byte) []byte {
	t.Helper()
	fj, _ := json.Marshal(list)
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	write := func(h tar.Header, b []byte) {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(b))
		}
		h.Mode = 0o644
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if len(b) > 0 {
			_, _ = tw.Write(b)
		}
	}
	write(tar.Header{Name: PluginManifestName, Typeflag: tar.TypeReg}, manifest)
	write(tar.Header{Name: PluginFilesName, Typeflag: tar.TypeReg}, fj)
	for _, e := range entries {
		write(e.hdr, e.body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func fileOf(p string, b []byte) PluginFile {
	s := sha256.Sum256(b)
	return PluginFile{Path: p, SHA256: hex.EncodeToString(s[:]), Size: int64(len(b))}
}

// 54.38 T01: a package unpacks with every file and its digest checked.
func TestUnpackPluginGood(t *testing.T) {
	pkg, sum := buildPlugin(t, samplePlugin())
	dest := filepath.Join(t.TempDir(), "p")
	got, err := UnpackPlugin(bytes.NewReader(pkg), dest, PluginExpect{FilesSHA256: sum})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.DeveloperSig) != "sig" || len(got.Files) != 5 {
		t.Fatalf("package: %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(dest, "i18n", "ar.json"))
	if err != nil || !strings.Contains(string(b), "س") {
		t.Fatalf("ar.json: %q %v", b, err)
	}
	for _, f := range []string{PluginManifestName, PluginFilesName, PluginDeveloperSig, "plugin.wasm"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(f))); err != nil {
			t.Fatalf("%s missing: %v", f, err)
		}
	}
}

func TestBuildPluginIsReproducible(t *testing.T) {
	a, _ := buildPlugin(t, samplePlugin())
	b, _ := buildPlugin(t, samplePlugin())
	if !bytes.Equal(a, b) {
		t.Fatal("two builds differ")
	}
}

// 54.01 T03 and 54.38 T02: hostile archives are refused with their code and leave nothing.
func TestUnpackPluginRefuses(t *testing.T) {
	man := []byte("schema = 1\n")
	good := []byte("ok")
	baseList := []PluginFile{fileOf("a.txt", good), fileOf(PluginManifestName, man)}
	many := make([]PluginFile, 0, PluginMaxFiles+1)
	for i := 0; i <= PluginMaxFiles; i++ {
		many = append(many, fileOf(fmt.Sprintf("f%04d", i), good))
	}
	many = append(many, fileOf(PluginManifestName, man))
	sort.Slice(many, func(i, j int) bool { return many[i].Path < many[j].Path })
	big := []PluginFile{{Path: "big.bin", SHA256: strings.Repeat("0", 64), Size: PluginMaxUnpackedBytes + 1}, fileOf(PluginManifestName, man)}
	cases := []struct {
		name    string
		list    []PluginFile
		entries []rawEntry
		man     []byte
		code    string
	}{
		{"dotdot", []PluginFile{fileOf("../../evil", good), fileOf(PluginManifestName, man)}, []rawEntry{{tar.Header{Name: "../../evil", Typeflag: tar.TypeReg}, good}}, man, CodePackageBadPath},
		{"dotdot unlisted", baseList, []rawEntry{{tar.Header{Name: "../../evil", Typeflag: tar.TypeReg}, good}}, man, CodePackageBadPath},
		{"absolute", baseList, []rawEntry{{tar.Header{Name: "/etc/passwd", Typeflag: tar.TypeReg}, good}}, man, CodePackageBadPath},
		{"symlink", baseList, []rawEntry{{tar.Header{Name: "a.txt", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, nil}}, man, CodePackageBadEntry},
		{"hardlink", baseList, []rawEntry{{tar.Header{Name: "a.txt", Typeflag: tar.TypeLink, Linkname: "manifest.toml"}, nil}}, man, CodePackageBadEntry},
		{"device", baseList, []rawEntry{{tar.Header{Name: "a.txt", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}, nil}}, man, CodePackageBadEntry},
		{"unlisted", baseList, []rawEntry{{tar.Header{Name: "a.txt", Typeflag: tar.TypeReg}, good}, {tar.Header{Name: "b.txt", Typeflag: tar.TypeReg}, good}}, man, CodePackageUnlisted},
		{"missing", baseList, nil, man, CodePackageMissingFile},
		{"too many files", many, nil, man, CodePackageTooManyFiles},
		{"too big unpacked", big, nil, man, CodePackageTooLarge},
		{"wrong sha256", []PluginFile{{Path: "a.txt", SHA256: strings.Repeat("1", 64), Size: 2}, fileOf(PluginManifestName, man)}, []rawEntry{{tar.Header{Name: "a.txt", Typeflag: tar.TypeReg}, good}}, man, CodePackageDigestMismatch},
		{"manifest changed", baseList, []rawEntry{{tar.Header{Name: "a.txt", Typeflag: tar.TypeReg}, good}}, []byte("schema = 2\n"), CodePackageDigestMismatch},
		{"not NFC", []PluginFile{fileOf("café.txt", good), fileOf(PluginManifestName, man)}, nil, man, CodePackageBadPath},
		{"long path", []PluginFile{fileOf(strings.Repeat("a", 201), good), fileOf(PluginManifestName, man)}, nil, man, CodePackageBadPath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pkg := rawPlugin(t, c.list, c.entries, c.man)
			dest := filepath.Join(t.TempDir(), "p")
			_, err := UnpackPlugin(bytes.NewReader(pkg), dest, PluginExpect{})
			if PluginCode(err) != c.code {
				t.Fatalf("want %s, got %v", c.code, err)
			}
			if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
				t.Fatalf("dest left behind: %v", statErr)
			}
		})
	}
}

// 54.01 T04: files.json's digest must be the one the signatures cover.
func TestUnpackPluginDigestMismatch(t *testing.T) {
	pkg, _ := buildPlugin(t, samplePlugin())
	_, err := UnpackPlugin(bytes.NewReader(pkg), filepath.Join(t.TempDir(), "p"), PluginExpect{FilesSHA256: strings.Repeat("a", 64)})
	if PluginCode(err) != CodePackageDigestMismatch {
		t.Fatalf("got %v", err)
	}
}

func TestUnpackPluginPackedLimit(t *testing.T) {
	r := &countingReader{r: bytes.NewReader(make([]byte, 10)), limit: 5}
	buf := make([]byte, 10)
	if _, err := r.Read(buf); PluginCode(err) != CodePackageTooLarge {
		t.Fatalf("got %v", err)
	}
}

