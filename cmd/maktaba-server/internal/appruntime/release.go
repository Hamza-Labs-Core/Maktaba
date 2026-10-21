package appruntime

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The release asset a headless runtime updates from (Epic 39 story 39.10,
// .github/workflows/release.yml): the payload as a tarball holding server/.
const (
	assetPrefix = "maktaba-linux-payload-"
	// AssetRoot is the payload's directory inside the tarball.
	AssetRoot = "server"
)

// ReleaseAsset is the name of the payload tarball of release version (a tag,
// with or without its "v") for this machine.
func ReleaseAsset(version string) string {
	arch := strings.TrimPrefix(Platform(), "linux-")
	return assetPrefix + strings.TrimPrefix(version, "v") + "-" + arch + ".tar.gz"
}

// Of is the runtime a payload directory is installed in: a directory with an
// .installed record (Install's) beside its payload.json. False for any other
// payload (the AppImage's, a package's, a build's).
func Of(payload string) (Runtime, bool) {
	if payload == "" {
		return Runtime{}, false
	}
	if _, err := readInstalled(payload); err != nil {
		return Runtime{}, false
	}
	return New(filepath.Dir(payload)), true
}

// maxUnpacked bounds what Unpack writes (a payload is well under 1 GB).
const maxUnpacked = 4 << 30

// Unpack extracts a payload tarball (gzip) into dir, which must be empty, and
// returns the payload directory in it. Only directories, regular files and
// symlinks are taken; a path that climbs out of dir or an absolute one is refused. Links are made last,
// so no file is written through one. Install then checks every file against
// the manifest.
func Unpack(tarball, dir string) (string, error) {
	f, err := os.Open(tarball)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrInvalid, filepath.Base(tarball), err)
	}
	defer gz.Close()
	type link struct{ name, target string }
	var links []link
	var total int64
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%w: %s: %v", ErrInvalid, filepath.Base(tarball), err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." {
			continue
		}
		// within dir: no absolute path and no ".." component that climbs out of it
		// (a file name may hold ".." itself, as "a..b")
		if !filepath.IsLocal(filepath.FromSlash(name)) || !inside(name) {
			return "", fmt.Errorf("%w: %s: %q leaves the payload", ErrInvalid, filepath.Base(tarball), h.Name)
		}
		out := filepath.Join(dir, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if total += h.Size; total > maxUnpacked {
				return "", fmt.Errorf("%w: %s unpacks to more than %d GB", ErrInvalid, filepath.Base(tarball), maxUnpacked>>30)
			}
			if err := writeEntry(out, tr, h); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			links = append(links, link{out, h.Linkname})
		default:
			return "", fmt.Errorf("%w: %s: %q is not a file, directory or symlink", ErrInvalid, filepath.Base(tarball), h.Name)
		}
	}
	for _, l := range links {
		if err := os.MkdirAll(filepath.Dir(l.name), 0o755); err != nil {
			return "", err
		}
		if err := os.Symlink(l.target, l.name); err != nil {
			return "", err
		}
	}
	root := filepath.Join(dir, AssetRoot)
	if _, err := os.Stat(filepath.Join(root, PayloadJSON)); err != nil {
		return "", fmt.Errorf("%w: %s holds no %s/%s", ErrInvalid, filepath.Base(tarball), AssetRoot, PayloadJSON)
	}
	return root, nil
}

func writeEntry(out string, r io.Reader, h *tar.Header) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0o755|0o600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(w, r, h.Size)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}
