package componentpack

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/text/unicode/norm"
)

// A plugin package (.mkplugin, Epic 54 stories 54.01 §1 and 54.38 §3) is the same
// container as a pack, a tar compressed with zstd, with its own head:
//
//	manifest.toml              the plugin's manifest (checked by shared/plugins/pluginmanifest)
//	files.json                 [{"path","sha256","size"}] for every other file, manifest.toml
//	                           included, sorted by path: what a signature covers (54.14)
//	signatures/developer.sig   optional, beside files.json (54.14; Phase 1 reads none)
//	signatures/store.sig       optional
//	the files                  regular files only, each listed in files.json
//
// UnpackPlugin shares Unpack's entry rules (no absolute paths, "..", links, devices or
// anything unlisted; sizes and sha256 checked while writing) and adds the plugin limits.
const (
	PluginManifestName = "manifest.toml"
	PluginFilesName    = "files.json"
	PluginDeveloperSig = "signatures/developer.sig"
	PluginStoreSig     = "signatures/store.sig"

	// Limits (54.01 §1).
	PluginMaxPackedBytes   = 50 << 20
	PluginMaxUnpackedBytes = 200 << 20
	PluginMaxFiles         = 500
	PluginMaxPathBytes     = 200
	pluginMaxHead          = 1 << 20
	pluginMaxSig           = 16 << 10
)

// Plugin package refusal codes (words in web/src/i18n as plugins.error.<code>).
const (
	CodePackageBadPath        = "package_bad_path"
	CodePackageBadEntry       = "package_bad_entry"
	CodePackageBadHead        = "package_bad_head"
	CodePackageDigestMismatch = "package_digest_mismatch"
	CodePackageTooLarge       = "package_too_large"
	CodePackageTooManyFiles   = "package_too_many_files"
	CodePackageUnlisted       = "package_unlisted_file"
	CodePackageMissingFile    = "package_missing_file"
)

// PluginError is a refusal of a plugin package, with its code.
type PluginError struct {
	Code string
	Msg  string
}

func (e *PluginError) Error() string { return e.Code + ": " + e.Msg }

// Unwrap lets errors.Is(err, ErrInvalid) hold for every refusal.
func (e *PluginError) Unwrap() error { return ErrInvalid }

func pluginErr(code, format string, a ...any) error {
	return &PluginError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// PluginCode answers the code of a plugin refusal ("" for other errors).
func PluginCode(err error) string {
	var pe *PluginError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// PluginFile is one line of files.json.
type PluginFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// PluginExpect is what the verifier (54.14) passed; an empty field is not checked.
type PluginExpect struct {
	// FilesSHA256 is the sha256 of files.json the signatures cover.
	FilesSHA256 string
}

// PluginPackage is what UnpackPlugin read.
type PluginPackage struct {
	Manifest    []byte
	FilesJSON   []byte
	FilesSHA256 string
	Files       []PluginFile
	// DeveloperSig and StoreSig are the signatures' bytes, nil when absent.
	DeveloperSig []byte
	StoreSig     []byte
}

// SafePluginPath: a pack's safe path, at most 200 bytes, valid UTF-8 in NFC, and not one
// of the head's own names.
func SafePluginPath(p string) bool {
	if len(p) > PluginMaxPathBytes || !utf8.ValidString(p) || !norm.NFC.IsNormalString(p) {
		return false
	}
	switch p {
	case PluginFilesName, PluginDeveloperSig, PluginStoreSig:
		return false
	}
	// SafePath refuses the pack head's names, which a plugin may use as ordinary files
	if p == ManifestName || p == SumsName {
		return true
	}
	return SafePath(p)
}

// countingReader stops a package bigger than the packed limit.
type countingReader struct {
	r     io.Reader
	n     int64
	limit int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.limit {
		return n, pluginErr(CodePackageTooLarge, "the package is over %d bytes", c.limit)
	}
	return n, err
}

// UnpackPlugin writes a .mkplugin's files into dest, which must not exist yet (its parent
// must). Every rule holds or nothing stays: dest is removed on any failure, and the error
// carries a code (PluginCode). Signatures are checked by the caller before (54.14); this
// reader only checks files.json's digest against want.
func UnpackPlugin(r io.Reader, dest string, want PluginExpect) (pkg *PluginPackage, err error) {
	if err := os.Mkdir(dest, 0o755); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	cr := &countingReader{r: r, limit: PluginMaxPackedBytes}
	zr, err := zstd.NewReader(cr, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxDecoderMemory))
	if err != nil {
		return nil, pluginErr(CodePackageBadHead, "not zstd: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	pkg = &PluginPackage{}
	next := func() (*tar.Header, error) {
		h, err := tr.Next()
		if err != nil && !errors.Is(err, io.EOF) {
			if PluginCode(err) != "" {
				return nil, err
			}
			return nil, pluginErr(CodePackageBadHead, "read: %v", err)
		}
		return h, err
	}
	readHeadFile := func(want string, limit int64) ([]byte, error) {
		h, err := next()
		if err != nil {
			return nil, pluginErr(CodePackageBadHead, "no %s", want)
		}
		if h.Name != want || h.Typeflag != tar.TypeReg {
			return nil, pluginErr(CodePackageBadHead, "the entry %q comes where %s must", h.Name, want)
		}
		if h.Size > limit {
			return nil, pluginErr(CodePackageTooLarge, "%s is over %d bytes", want, limit)
		}
		return io.ReadAll(io.LimitReader(tr, limit))
	}
	if pkg.Manifest, err = readHeadFile(PluginManifestName, pluginMaxHead); err != nil {
		return nil, err
	}
	if pkg.FilesJSON, err = readHeadFile(PluginFilesName, pluginMaxHead); err != nil {
		return nil, err
	}
	pkg.FilesSHA256 = sha256Hex(pkg.FilesJSON)
	if want.FilesSHA256 != "" && pkg.FilesSHA256 != want.FilesSHA256 {
		return nil, pluginErr(CodePackageDigestMismatch, "files.json is %s, the signature covers %s", pkg.FilesSHA256, want.FilesSHA256)
	}
	dec := json.NewDecoder(bytes.NewReader(pkg.FilesJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pkg.Files); err != nil {
		return nil, pluginErr(CodePackageBadHead, "files.json: %v", err)
	}
	if len(pkg.Files) > PluginMaxFiles {
		return nil, pluginErr(CodePackageTooManyFiles, "%d files, at most %d", len(pkg.Files), PluginMaxFiles)
	}
	listed := make(map[string]PluginFile, len(pkg.Files))
	var total int64
	for i, f := range pkg.Files {
		if !SafePluginPath(f.Path) {
			return nil, pluginErr(CodePackageBadPath, "bad path %q", f.Path)
		}
		if i > 0 && pkg.Files[i-1].Path >= f.Path {
			return nil, pluginErr(CodePackageBadHead, "files.json is not sorted by path, or lists %q twice", f.Path)
		}
		if !hexRe.MatchString(f.SHA256) || f.Size < 0 {
			return nil, pluginErr(CodePackageBadHead, "%s: bad sha256 or size", f.Path)
		}
		total += f.Size
		listed[f.Path] = f
	}
	if total > PluginMaxUnpackedBytes {
		return nil, pluginErr(CodePackageTooLarge, "%d bytes unpacked, at most %d", total, PluginMaxUnpackedBytes)
	}
	mf, ok := listed[PluginManifestName]
	if !ok {
		return nil, pluginErr(CodePackageMissingFile, "files.json does not list %s", PluginManifestName)
	}
	if sha256Hex(pkg.Manifest) != mf.SHA256 || int64(len(pkg.Manifest)) != mf.Size {
		return nil, pluginErr(CodePackageDigestMismatch, "%s does not match files.json", PluginManifestName)
	}
	if err := os.WriteFile(filepath.Join(dest, PluginManifestName), pkg.Manifest, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dest, PluginFilesName), pkg.FilesJSON, 0o644); err != nil {
		return nil, err
	}
	done := map[string]bool{PluginManifestName: true}
	for {
		h, err := next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name := h.Name
		if h.Typeflag == tar.TypeDir {
			if !SafePath(path.Clean(name)) {
				return nil, pluginErr(CodePackageBadPath, "bad folder path %q", name)
			}
			continue
		}
		if name == PluginDeveloperSig || name == PluginStoreSig {
			if h.Typeflag != tar.TypeReg || h.Size > pluginMaxSig {
				return nil, pluginErr(CodePackageBadEntry, "%s is not a small regular file", name)
			}
			b, err := io.ReadAll(io.LimitReader(tr, pluginMaxSig))
			if err != nil {
				return nil, pluginErr(CodePackageBadHead, "%s: %v", name, err)
			}
			if name == PluginDeveloperSig {
				pkg.DeveloperSig = b
			} else {
				pkg.StoreSig = b
			}
			if err := writeFile(dest, Entry{Kind: 'f', Mode: 0o644, Size: int64(len(b)), SHA256: sha256Hex(b), Path: name}, bytes.NewReader(b)); err != nil {
				return nil, err
			}
			continue
		}
		// SafePluginPath already refuses these; checked again where the name meets the
		// disk, so no entry can ever be written outside dest
		if !SafePluginPath(name) || strings.Contains(name, "..") || filepath.IsAbs(name) {
			return nil, pluginErr(CodePackageBadPath, "bad path %q", name)
		}
		if h.Typeflag != tar.TypeReg {
			return nil, pluginErr(CodePackageBadEntry, "%s: entry type %q is not a regular file", name, string(h.Typeflag))
		}
		f, ok := listed[name]
		if !ok {
			return nil, pluginErr(CodePackageUnlisted, "%s is not listed in files.json", name)
		}
		if done[name] {
			return nil, pluginErr(CodePackageBadEntry, "%s comes twice", name)
		}
		done[name] = true
		if h.Size != f.Size {
			return nil, pluginErr(CodePackageDigestMismatch, "%s: %d bytes, files.json lists %d", name, h.Size, f.Size)
		}
		if err := writeFile(dest, Entry{Kind: 'f', Mode: 0o644, Size: f.Size, SHA256: f.SHA256, Path: name}, tr); err != nil {
			if errors.Is(err, ErrInvalid) && PluginCode(err) == "" {
				return nil, pluginErr(CodePackageDigestMismatch, "%v", err)
			}
			return nil, err
		}
	}
	for _, f := range pkg.Files {
		if !done[f.Path] {
			return nil, pluginErr(CodePackageMissingFile, "%s is listed in files.json but not in the package", f.Path)
		}
	}
	return pkg, nil
}

// BuildPlugin writes a .mkplugin from files (path → bytes), byte for byte the same from the
// same files: manifest.toml, files.json, the signatures given, then the files sorted.
// files must hold manifest.toml; files.json and the signatures are made or taken from sigs.
func BuildPlugin(w io.Writer, files map[string][]byte, sigs map[string][]byte) (filesSHA256 string, err error) {
	man, ok := files[PluginManifestName]
	if !ok {
		return "", pluginErr(CodePackageMissingFile, "no %s", PluginManifestName)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	list := make([]PluginFile, 0, len(paths))
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		list = append(list, PluginFile{Path: p, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(files[p]))})
	}
	fj, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	zw, err := zstd.NewWriter(w, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return "", err
	}
	tw := tar.NewWriter(zw)
	add := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	if err := add(PluginManifestName, man); err != nil {
		return "", err
	}
	if err := add(PluginFilesName, fj); err != nil {
		return "", err
	}
	for _, s := range []string{PluginDeveloperSig, PluginStoreSig} {
		if b, ok := sigs[s]; ok {
			if err := add(s, b); err != nil {
				return "", err
			}
		}
	}
	for _, p := range paths {
		if p == PluginManifestName {
			continue
		}
		if err := add(p, files[p]); err != nil {
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return sha256Hex(fj), nil
}
