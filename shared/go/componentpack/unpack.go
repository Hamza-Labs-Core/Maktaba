package componentpack

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

// Expect is what the signed index says of a pack; an empty field is not checked.
type Expect struct {
	ID          string
	Version     string
	Platform    string
	FilesSHA256 string
	// UnpackedBytes caps what is written (0: pack.json's own figure).
	UnpackedBytes int64
}

// Unpack writes a pack's files into dest, which must not exist yet (its parent must).
// Every rule of README §6.4 holds or nothing stays: dest is removed on any failure.
//
//   - pack.json and files.sha256 come first; files.sha256 is the one pack.json names and,
//     when want says, the one the signed index names;
//   - every other entry is a regular file or a symlink that files.sha256 lists, at a
//     clean relative path with no "..", never twice; a folder entry is skipped (folders
//     are made from the paths); a hard link, device, pipe or anything else is refused;
//   - a file is written only under real folders (never through a link), with its listed
//     mode's permission bits only, and its size and sha256 are checked as it is written;
//   - a link's target stays inside dest; links are made last, after every file;
//   - nothing listed is missing at the end.
func Unpack(r io.Reader, dest string, want Expect) (m *Manifest, err error) {
	if err := os.Mkdir(dest, 0o755); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxDecoderMemory))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	man, entries, _, err := readHead(tr)
	if err != nil {
		return nil, err
	}
	switch {
	case want.ID != "" && man.ID != want.ID,
		want.Version != "" && man.Version != want.Version,
		want.Platform != "" && man.Platform != want.Platform:
		return nil, invalid("the pack is %s %s %s, expected %s %s %s", man.ID, man.Version, man.Platform, want.ID, want.Version, want.Platform)
	case want.FilesSHA256 != "" && man.FilesSHA256 != want.FilesSHA256:
		return nil, invalid("files.sha256 is %s, the index says %s", man.FilesSHA256, want.FilesSHA256)
	case want.UnpackedBytes > 0 && man.UnpackedBytes != want.UnpackedBytes:
		return nil, invalid("unpacked_bytes %d, the index says %d", man.UnpackedBytes, want.UnpackedBytes)
	}
	listed := make(map[string]Entry, len(entries))
	for _, e := range entries {
		listed[e.Path] = e
	}
	done := map[string]bool{}
	var links []Entry
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalid("read: %v", err)
		}
		name := h.Name
		if h.Typeflag == tar.TypeDir {
			if !SafePath(path.Clean(name)) {
				return nil, invalid("bad folder path %q", name)
			}
			continue
		}
		if !SafePath(name) {
			return nil, invalid("bad path %q", name)
		}
		e, ok := listed[name]
		if !ok {
			return nil, invalid("%s is not listed in %s", name, SumsName)
		}
		if done[name] {
			return nil, invalid("%s comes twice", name)
		}
		done[name] = true
		switch h.Typeflag {
		case tar.TypeReg:
			if e.Kind != 'f' {
				return nil, invalid("%s is a file, %s lists a link", name, SumsName)
			}
			if h.Size != e.Size {
				return nil, invalid("%s: %d bytes, %s lists %d", name, h.Size, SumsName, e.Size)
			}
			if err := writeFile(dest, e, tr); err != nil {
				return nil, err
			}
		case tar.TypeSymlink:
			if e.Kind != 'l' || h.Linkname != e.Target {
				return nil, invalid("%s: link to %q, %s lists %q", name, h.Linkname, SumsName, e.Target)
			}
			links = append(links, e)
		default:
			return nil, invalid("%s: entry type %q is not a file or a link", name, string(h.Typeflag))
		}
	}
	for _, e := range entries {
		if !done[e.Path] {
			return nil, invalid("%s is listed in %s but not in the pack", e.Path, SumsName)
		}
	}
	for _, e := range links {
		if err := makeParents(dest, e.Path); err != nil {
			return nil, err
		}
		if err := os.Symlink(filepath.FromSlash(e.Target), filepath.Join(dest, filepath.FromSlash(e.Path))); err != nil {
			return nil, err
		}
	}
	return man, nil
}

// makeParents makes rel's folders under dest, refusing to go through anything that is not
// a real folder (a link made by an earlier entry, say).
func makeParents(dest, rel string) error {
	dir := path.Dir(rel)
	if dir == "." {
		return nil
	}
	cur := dest
	for _, el := range splitPath(dir) {
		cur = filepath.Join(cur, el)
		info, err := os.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case !info.IsDir():
			return invalid("%s: %s is not a folder", rel, el)
		}
	}
	return nil
}

func splitPath(p string) []string {
	var out []string
	for p != "." && p != "" {
		out = append([]string{path.Base(p)}, out...)
		p = path.Dir(p)
	}
	return out
}

func writeFile(dest string, e Entry, r io.Reader) error {
	if err := makeParents(dest, e.Path); err != nil {
		return err
	}
	p := filepath.Join(dest, filepath.FromSlash(e.Path))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, e.Mode.Perm()&0o755|0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, e.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != e.Size {
		return invalid("%s: %d bytes, %s lists %d", e.Path, n, SumsName, e.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != e.SHA256 {
		return invalid("%s: sha256 %s, %s lists %s", e.Path, got, SumsName, e.SHA256)
	}
	// the umask may have taken bits off: set the listed ones (never setuid or setgid)
	if err := os.Chmod(p, e.Mode.Perm()&0o755|0o600); err != nil {
		return fmt.Errorf("componentpack: %s: %w", e.Path, err)
	}
	return nil
}
