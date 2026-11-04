package componentpack

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Manifest is pack.json.
type Manifest struct {
	Schema        int    `json:"schema"`
	ID            string `json:"id"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	FilesSHA256   string `json:"files_sha256"`
	UnpackedBytes int64  `json:"unpacked_bytes"`
}

// DefaultPartSize is the largest part a pack is uploaded as: GitHub refuses a release
// asset of 2 GiB or more, so a bigger pack is split into .part1, .part2, ...
const DefaultPartSize int64 = 1900 << 20

// Options say what to pack.
type Options struct {
	Dir      string // the tree to pack (its files and links)
	ID       string // the asset component
	Version  string // the release version, without "v"
	Platform string // darwin-arm64, windows-amd64, linux-amd64, linux-arm64
	// ModTime is every entry's time: the commit's (SOURCE_DATE_EPOCH).
	ModTime time.Time
	Out     string // the folder the pack (or its parts) is written to
	// PartSize splits a pack larger than this (0: DefaultPartSize).
	PartSize int64
}

// Part is one file of a split pack.
type Part struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Built is a written pack: its name, digest and size as a whole (the parts joined, when
// it is split), and pack.json.
type Built struct {
	Name     string
	SHA256   string
	Size     int64
	Parts    []Part // empty when the pack is one file
	Manifest Manifest
}

var (
	idRe       = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_-]+)+$`)
	versionRe  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	platformRe = regexp.MustCompile(`^(darwin|windows|linux)-(arm64|amd64)$`)
)

// FileName is a pack's name: <id>-<version>-<platform>.tar.zst.
func FileName(id, version, platform string) string {
	return id + "-" + version + "-" + platform + ".tar.zst"
}

// PartName is the n-th part's name (n from 1).
func PartName(name string, n int) string { return fmt.Sprintf("%s.part%d", name, n) }

// Build writes the pack of o.Dir: the same tree and time give the same bytes (sorted
// entries, uid and gid 0, no user or group names, one time, zstd at its best level on
// one thread).
func Build(o Options) (*Built, error) {
	if !idRe.MatchString(o.ID) || !versionRe.MatchString(o.Version) || !platformRe.MatchString(o.Platform) {
		return nil, fmt.Errorf("componentpack: bad id %q, version %q or platform %q", o.ID, o.Version, o.Platform)
	}
	entries, total, err := Scan(o.Dir)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("componentpack: %s is empty", o.Dir)
	}
	sums := FormatSums(entries)
	man := Manifest{Schema: Schema, ID: o.ID, Version: o.Version, Platform: o.Platform,
		FilesSHA256: sha256Hex(sums), UnpackedBytes: total}
	mj, err := json.Marshal(man)
	if err != nil {
		return nil, err
	}
	mj = append(mj, '\n')
	partSize := o.PartSize
	if partSize <= 0 {
		partSize = DefaultPartSize
	}
	name := FileName(o.ID, o.Version, o.Platform)
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return nil, err
	}
	// a re-run starts clean: no stale part of an earlier, bigger build stays behind
	if err := removePack(o.Out, name); err != nil {
		return nil, err
	}
	sw := &splitWriter{dir: o.Out, name: name, limit: partSize, whole: sha256.New()}
	err = func() error {
		zw, err := zstd.NewWriter(sw, zstd.WithEncoderLevel(zstd.SpeedBestCompression),
			zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
		if err != nil {
			return err
		}
		tw := tar.NewWriter(zw)
		mt := o.ModTime.UTC().Truncate(time.Second)
		hdr := func(name string, mode int64, size int64) *tar.Header {
			return &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: size, ModTime: mt, Format: tar.FormatPAX}
		}
		for _, f := range []struct {
			name string
			b    []byte
		}{{ManifestName, mj}, {SumsName, sums}} {
			if err := tw.WriteHeader(hdr(f.name, 0o644, int64(len(f.b)))); err != nil {
				return err
			}
			if _, err := tw.Write(f.b); err != nil {
				return err
			}
		}
		for _, e := range entries {
			if e.Kind == 'l' {
				h := &tar.Header{Typeflag: tar.TypeSymlink, Name: e.Path, Linkname: e.Target, Mode: 0o777, ModTime: mt, Format: tar.FormatPAX}
				if err := tw.WriteHeader(h); err != nil {
					return err
				}
				continue
			}
			if err := tw.WriteHeader(hdr(e.Path, int64(e.Mode.Perm()), e.Size)); err != nil {
				return err
			}
			if err := copyChecked(tw, filepath.Join(o.Dir, filepath.FromSlash(e.Path)), e); err != nil {
				return err
			}
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return zw.Close()
	}()
	if cerr := sw.close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = removePack(o.Out, name)
		return nil, err
	}
	b := &Built{Name: name, SHA256: hex.EncodeToString(sw.whole.Sum(nil)), Size: sw.total, Manifest: man}
	if len(sw.parts) == 1 {
		if err := os.Rename(filepath.Join(o.Out, sw.parts[0].Name), filepath.Join(o.Out, name)); err != nil {
			return nil, err
		}
	} else {
		b.Parts = sw.parts
	}
	return b, nil
}

// copyChecked copies a file into the tar, refusing one that changed since Scan hashed it.
func copyChecked(w io.Writer, p string, e Entry) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(f, e.Size+1))
	if err != nil {
		return err
	}
	if n != e.Size || hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
		return fmt.Errorf("componentpack: %s changed while it was packed", e.Path)
	}
	return nil
}

func removePack(dir, name string) error {
	if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(dir, name+".part*"))
	if err != nil {
		return err
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			return err
		}
	}
	return nil
}

// splitWriter writes name.part1, name.part2, ... of at most limit bytes each, and hashes
// every part and the whole.
type splitWriter struct {
	dir, name string
	limit     int64
	whole     hash.Hash
	total     int64
	parts     []Part
	f         *os.File
	h         hash.Hash
	n         int64
}

func (s *splitWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if s.f == nil || s.n == s.limit {
			if err := s.next(); err != nil {
				return written, err
			}
		}
		chunk := p
		if room := s.limit - s.n; int64(len(chunk)) > room {
			chunk = chunk[:room]
		}
		k, err := s.f.Write(chunk)
		s.h.Write(chunk[:k])
		s.whole.Write(chunk[:k])
		s.n += int64(k)
		s.total += int64(k)
		written += k
		if err != nil {
			return written, err
		}
		p = p[k:]
	}
	return written, nil
}

func (s *splitWriter) next() error {
	if err := s.finish(); err != nil {
		return err
	}
	name := PartName(s.name, len(s.parts)+1)
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	s.f, s.h, s.n = f, sha256.New(), 0
	s.parts = append(s.parts, Part{Name: name})
	return nil
}

func (s *splitWriter) finish() error {
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	last := &s.parts[len(s.parts)-1]
	last.SHA256, last.Size = hex.EncodeToString(s.h.Sum(nil)), s.n
	s.f = nil
	return err
}

func (s *splitWriter) close() error { return s.finish() }

// Open reads a pack in dir by its name: the file, or its parts joined in order
// (name.part1, name.part2, ...). It returns the reader, the part names read (none for one
// file), and a closer.
func Open(dir, name string) (io.Reader, []string, func() error, error) {
	if f, err := os.Open(filepath.Join(dir, name)); err == nil {
		return f, nil, f.Close, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, err
	}
	var files []*os.File
	var names []string
	closeAll := func() error {
		var first error
		for _, f := range files {
			if err := f.Close(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	for n := 1; ; n++ {
		pn := PartName(name, n)
		f, err := os.Open(filepath.Join(dir, pn))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			_ = closeAll()
			return nil, nil, nil, err
		}
		files = append(files, f)
		names = append(names, pn)
	}
	if len(files) == 0 {
		return nil, nil, nil, fmt.Errorf("componentpack: %s: %w", name, os.ErrNotExist)
	}
	readers := make([]io.Reader, len(files))
	for i, f := range files {
		readers[i] = f
	}
	return io.MultiReader(readers...), names, closeAll, nil
}

// ReadHead reads a pack's pack.json and files.sha256 (its first two entries) without
// reading the files, and checks that files.sha256 is the one pack.json names.
func ReadHead(r io.Reader) (*Manifest, []Entry, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxDecoderMemory))
	if err != nil {
		return nil, nil, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	man, entries, _, err := readHead(tr)
	return man, entries, err
}

const (
	maxManifest      = 64 << 10
	maxSums          = 64 << 20
	maxDecoderMemory = 1 << 30
)

func readHead(tr *tar.Reader) (*Manifest, []Entry, []byte, error) {
	mj, err := readSmall(tr, ManifestName, maxManifest)
	if err != nil {
		return nil, nil, nil, err
	}
	var man Manifest
	dec := json.NewDecoder(strings.NewReader(string(mj)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&man); err != nil {
		return nil, nil, nil, invalid("%s: %v", ManifestName, err)
	}
	if man.Schema != Schema {
		return nil, nil, nil, invalid("%s: schema %d, this build reads %d", ManifestName, man.Schema, Schema)
	}
	if !hexRe.MatchString(man.FilesSHA256) || man.UnpackedBytes < 0 {
		return nil, nil, nil, invalid("%s: bad files_sha256 or unpacked_bytes", ManifestName)
	}
	sums, err := readSmall(tr, SumsName, maxSums)
	if err != nil {
		return nil, nil, nil, err
	}
	if got := sha256Hex(sums); got != man.FilesSHA256 {
		return nil, nil, nil, invalid("%s has sha256 %s, %s says %s", SumsName, got, ManifestName, man.FilesSHA256)
	}
	entries, err := ParseSums(sums)
	if err != nil {
		return nil, nil, nil, err
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	if total != man.UnpackedBytes {
		return nil, nil, nil, invalid("%s lists %d bytes, %s says %d", SumsName, total, ManifestName, man.UnpackedBytes)
	}
	return &man, entries, sums, nil
}

func readSmall(tr *tar.Reader, want string, limit int64) ([]byte, error) {
	h, err := tr.Next()
	if err != nil {
		return nil, invalid("no %s: %v", want, err)
	}
	if h.Name != want || h.Typeflag != tar.TypeReg {
		return nil, invalid("the entry %q comes where %s must", h.Name, want)
	}
	if h.Size > limit {
		return nil, invalid("%s is larger than %d bytes", want, limit)
	}
	b, err := io.ReadAll(io.LimitReader(tr, limit+1))
	if err != nil {
		return nil, invalid("%s: %v", want, err)
	}
	return b, nil
}
