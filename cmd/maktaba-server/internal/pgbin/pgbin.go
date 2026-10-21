// Package pgbin fetches the Postgres a machine runs its copy of the home's database
// with (specs/epics/37-node-replica): the build the home bundles (Maktaba.app,
// tools/release/mac-app/pins.env), theseus-rs/postgresql-binaries 16.15.0 for this
// machine's platform. Every archive is checked against its pinned SHA-256 and size
// before it is unpacked, into <root>/postgresql-<version>-<triple>, once.
package pgbin

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Version is the Postgres every machine runs (the home's major: a copy needs it).
const Version = "16.15.0"

// Major is Version's major.
const Major = "16"

const base = "https://github.com/theseus-rs/postgresql-binaries/releases/download/" + Version + "/"

// Pin is one platform's archive.
type Pin struct {
	Triple string
	SHA256 string
	Size   int64
	Zip    bool
}

// URL is where the archive is downloaded from.
func (p Pin) URL() string { return base + p.Name() }

// Name is the archive's file name.
func (p Pin) Name() string {
	ext := ".tar.gz"
	if p.Zip {
		ext = ".zip"
	}
	return "postgresql-" + Version + "-" + p.Triple + ext
}

// Dir is the unpacked install's directory name.
func (p Pin) Dir() string { return "postgresql-" + Version + "-" + p.Triple }

// pins are the archives, by GOOS/GOARCH. darwin/arm64 is the one Maktaba.app ships
// (pins.env; a test keeps them equal).
var pins = map[string]Pin{
	"windows/amd64": {Triple: "x86_64-pc-windows-msvc", Zip: true, Size: 46890560,
		SHA256: "98fe43cff00f5aef4a7669adb575655e913dc65b1fdfb997c46fe3c1e07f4efb"},
	"linux/amd64": {Triple: "x86_64-unknown-linux-gnu", Size: 11087052,
		SHA256: "77dd669eda3985ea8be26256f6af28d3c8414152ada713797555bb7423b4486a"},
	"linux/arm64": {Triple: "aarch64-unknown-linux-gnu", Size: 10897360,
		SHA256: "f6d49ffbaea28cf8732785b18afb75bded905667d4ec8e2308a13a30fe0a24d1"},
	"darwin/arm64": {Triple: "aarch64-apple-darwin", Size: 12107763,
		SHA256: "46f6382024d9b633d1f4b4903ffef8c2404e00ae83098d628c00591048cb0512"},
	"darwin/amd64": {Triple: "x86_64-apple-darwin", Size: 12408962,
		SHA256: "dfae21b2b931f1d8d1103afb6e8e108fdd94c6f407ce24d35b6cd0ad34f286d1"},
}

// For is the archive of a platform.
func For(goos, goarch string) (Pin, bool) {
	p, ok := pins[goos+"/"+goarch]
	return p, ok
}

// Here is this machine's archive.
func Here() (Pin, bool) { return For(runtime.GOOS, runtime.GOARCH) }

// Exe is a program's file name on this OS.
func Exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Installed reports the install at home has the programs a copy needs.
func Installed(home string) bool {
	for _, b := range []string{"postgres", "initdb", "pg_ctl", "pg_basebackup", "psql"} {
		if info, err := os.Stat(filepath.Join(home, "bin", Exe(b))); err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// Fetcher downloads an archive (tests replace it).
type Fetcher func(ctx context.Context, url string, w io.Writer) error

// HTTPFetch is the real download.
func HTTPFetch(client *http.Client) Fetcher {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	return func(ctx context.Context, url string, w io.Writer) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: %s", url, resp.Status)
		}
		_, err = io.Copy(w, resp.Body)
		return err
	}
}

// Ensure returns the install of pin under root, downloading, checking and unpacking
// it when it is not there yet (a half-done unpack is never taken for an install).
func Ensure(ctx context.Context, root string, pin Pin, fetch Fetcher, log io.Writer) (string, error) {
	home := filepath.Join(root, pin.Dir())
	if Installed(home) {
		return home, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(root, ".download-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	fmt.Fprintf(log, "[postgres] downloading PostgreSQL %s for this machine (%s, %d MB)\n", Version, pin.Triple, pin.Size>>20)
	h := sha256.New()
	if err := fetch(ctx, pin.URL(), io.MultiWriter(tmp, h)); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("download PostgreSQL %s: %w", Version, err)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != pin.SHA256 {
		return "", fmt.Errorf("PostgreSQL %s archive: SHA-256 %s, pinned %s: not used", Version, sum, pin.SHA256)
	}
	if info, err := os.Stat(tmp.Name()); err != nil || info.Size() != pin.Size {
		return "", fmt.Errorf("PostgreSQL %s archive: unexpected size", Version)
	}
	stage, err := os.MkdirTemp(root, ".unpack-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if pin.Zip {
		err = unzip(tmp.Name(), stage)
	} else {
		err = untargz(tmp.Name(), stage)
	}
	if err != nil {
		return "", fmt.Errorf("unpack PostgreSQL %s: %w", Version, err)
	}
	got := filepath.Join(stage, pin.Dir())
	if !Installed(got) {
		return "", errors.New("unexpected PostgreSQL archive layout")
	}
	if err := os.RemoveAll(home); err != nil {
		return "", err
	}
	if err := os.Rename(got, home); err != nil {
		return "", err
	}
	return home, nil
}

// inside joins name under dir, refusing a path that leaves it.
func inside(dir, name string) (string, error) {
	root := filepath.Clean(dir) + string(filepath.Separator)
	p := filepath.Join(dir, filepath.FromSlash(name))
	if p != filepath.Clean(dir) && !strings.HasPrefix(p, root) {
		return "", fmt.Errorf("archive entry %q leaves the install", name)
	}
	return p, nil
}

func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p, err := inside(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(p, rc, f.Mode().Perm()|0o600)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func untargz(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := inside(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := writeFile(p, tr, os.FileMode(hdr.Mode).Perm()|0o600); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// a library's versioned name: only a link to a file beside it
			target := filepath.Join(filepath.Dir(p), hdr.Linkname)
			if hdr.Linkname == "" || hdr.Linkname == "." || hdr.Linkname == ".." ||
				strings.ContainsAny(hdr.Linkname, `/\`) || filepath.Dir(target) != filepath.Dir(p) ||
				!strings.HasPrefix(target, filepath.Clean(dir)+string(filepath.Separator)) {
				return fmt.Errorf("archive link %q leaves its directory", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			_ = os.Remove(p)
			if err := os.Symlink(hdr.Linkname, p); err != nil {
				return err
			}
		}
	}
}

func writeFile(p string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
