//go:build !windows

package appruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarEntry is one entry of a test tarball: a file (body), a directory (dir) or
// a symlink (link).
type tarEntry struct {
	name, body, link string
	dir              bool
	mode             int64
}

func writeTarball(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode}
		switch {
		case e.dir:
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		case e.link != "":
			h.Typeflag, h.Linkname = tar.TypeSymlink, e.link
		default:
			h.Typeflag, h.Size = tar.TypeReg, int64(len(e.body))
			if h.Mode == 0 {
				h.Mode = 0o644
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "payload.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// tarPayload packs a payload directory as release.yml does: under server/.
func tarPayload(t *testing.T, payload string) string {
	t.Helper()
	var entries []tarEntry
	err := filepath.Walk(payload, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(payload, p)
		name := filepath.ToSlash(filepath.Join(AssetRoot, rel))
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			entries = append(entries, tarEntry{name: name, link: target})
		case info.IsDir():
			entries = append(entries, tarEntry{name: name + "/", dir: true})
		default:
			b, _ := os.ReadFile(p)
			entries = append(entries, tarEntry{name: name, body: string(b), mode: int64(info.Mode().Perm())})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return writeTarball(t, entries)
}

func TestUnpackThenInstall(t *testing.T) {
	tarball := tarPayload(t, samplePayload(t, "v0.4.0"))
	payload, err := Unpack(tarball, filepath.Join(t.TempDir(), "x"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(payload) != AssetRoot {
		t.Errorf("payload %s", payload)
	}
	r := testRuntime(t)
	res, err := r.Install(context.Background(), payload, nil, Options{})
	if err != nil {
		t.Fatalf("install of the unpacked payload: %v", err)
	}
	if tgt, err := os.Readlink(filepath.Join(res.Dir, "python", "bin", "python3")); err != nil || tgt != "python3.12" {
		t.Errorf("symlink %q %v", tgt, err)
	}
	if rt, ok := Of(res.Dir); !ok || rt.Root != r.Root {
		t.Errorf("Of(%s) = %v %v", res.Dir, rt.Root, ok)
	}
	if _, ok := Of(payload); ok {
		t.Error("a payload that is not installed is no runtime")
	}
}

func TestUnpackRefuses(t *testing.T) {
	for name, entries := range map[string][]tarEntry{
		"a path out":       {{name: "server/payload.json", body: "{}"}, {name: "../escape", body: "x"}},
		"an absolute path": {{name: "/etc/x", body: "x"}},
		"a .. that stays":  {{name: "server/bin/../payload.json", body: "{}"}},
		"a device":         nil, // below
		"a file through a link": {{name: "server/payload.json", body: "{}"}, {name: "server/lib", link: "/tmp"},
			{name: "server/lib/x", body: "x"}},
		"no payload.json": {{name: "server/bin/x", body: "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			var tarball string
			if entries == nil {
				var buf bytes.Buffer
				gz := gzip.NewWriter(&buf)
				tw := tar.NewWriter(gz)
				_ = tw.WriteHeader(&tar.Header{Name: "server/dev", Typeflag: tar.TypeChar, Mode: 0o644})
				_ = tw.Close()
				_ = gz.Close()
				tarball = filepath.Join(t.TempDir(), "dev.tar.gz")
				_ = os.WriteFile(tarball, buf.Bytes(), 0o644)
			} else {
				tarball = writeTarball(t, entries)
			}
			out := filepath.Join(t.TempDir(), "x")
			if _, err := Unpack(tarball, out); err == nil {
				t.Fatal("accepted")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(out), "escape")); !errors.Is(err, os.ErrNotExist) {
				t.Error("wrote outside the directory")
			}
		})
	}
}

func TestReleaseAsset(t *testing.T) {
	arch := strings.TrimPrefix(Platform(), "linux-")
	for _, v := range []string{"v1.5.0", "1.5.0"} {
		if got := ReleaseAsset(v); got != "maktaba-linux-payload-1.5.0-"+arch+".tar.gz" {
			t.Errorf("%s: %s", v, got)
		}
	}
}
