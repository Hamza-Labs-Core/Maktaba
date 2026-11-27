package pgbundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const archiveMarker = "compressed.json"

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func openChunk(path string) (io.Reader, func(), error) {
	return openArchiveInput(path, strings.HasSuffix(path, ".zst"))
}

func openArchiveInput(path string, compressed bool) (io.Reader, func(), error) {
	f, err := os.Open(path) //nolint:gosec // a dump's own file
	if err != nil {
		return nil, nil, err
	}
	closeFile := func() { _ = f.Close() }
	if !compressed {
		return f, closeFile, nil
	}
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		closeFile()
		return nil, nil, err
	}
	return dec, func() { dec.Close(); closeFile() }, nil
}

// ExpandChunk streams a chunk to dst for clients that require an ordinary file.
func ExpandChunk(ctx context.Context, dst io.Writer, path string) error {
	r, closeInput, err := openChunk(path)
	if err != nil {
		return err
	}
	defer closeInput()
	_, err = io.Copy(dst, contextReader{ctx, r})
	return err
}

func (b Bundle) compressBackups(ctx context.Context, now time.Time) error {
	days := b.nightly().CompressAfterDays
	if days <= 0 || BackupsHeld(b.Backups) {
		return nil
	}
	dumps, err := b.backups()
	if err != nil {
		return err
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	for _, dump := range dumps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if BackupsHeld(b.Backups) {
			return nil
		}
		if dump.directory && dump.at.Before(cutoff) {
			if err := archiveDump(ctx, filepath.Join(b.Backups, dump.name), now); err != nil {
				return err
			}
		}
	}
	return nil
}

func archiveDump(ctx context.Context, dir string, now time.Time) (err error) {
	marker := filepath.Join(dir, archiveMarker)
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	at := scheduledAt(filepath.Base(dir), info.ModTime())
	defer func() { err = errors.Join(err, os.Chtimes(dir, at, at)) }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".partial") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if BackupsHeld(filepath.Dir(dir)) {
			return nil
		}
		if !e.Type().IsRegular() {
			continue
		}
		name := e.Name()
		path := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(name, chunkPrefix) && strings.HasSuffix(name, chunkExt):
			if err := archiveFile(ctx, path, path+".zst", false); err != nil {
				return err
			}
		case strings.HasSuffix(name, ".dat.zst"):
			if err := archiveFile(ctx, path, path, true); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Version int       `json:"version"`
		At      time.Time `json:"at"`
		Algo    string    `json:"algo"`
	}{1, now.UTC(), "zstd"})
	if err != nil {
		return err
	}
	partial := marker + ".partial"
	defer func() { _ = os.Remove(partial) }()
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the dump's marker
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if BackupsHeld(filepath.Dir(dir)) {
		return nil
	}
	return os.Rename(partial, marker)
}

func archiveFile(ctx context.Context, src, dst string, compressed bool) error {
	// A crash between publishing the archive and removing the raw chunk leaves both.
	// The raw file remains authoritative until its replacement has been verified.
	partial := dst + ".partial"
	if compressed {
		partial = dst + ".recomp.partial"
	}
	defer func() { _ = os.Remove(partial) }()
	if err := encodeArchive(ctx, src, partial, compressed); err != nil {
		return err
	}
	if compressed {
		before, err := os.Stat(src)
		if err != nil {
			return err
		}
		after, err := os.Stat(partial)
		if err != nil {
			return err
		}
		if after.Size() >= before.Size() {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backups := filepath.Dir(filepath.Dir(src))
	if BackupsHeld(backups) {
		return nil
	}
	if err := os.Rename(partial, dst); err != nil {
		return err
	}
	if !compressed && !BackupsHeld(backups) {
		return os.Remove(src)
	}
	return nil
}

func encodeArchive(ctx context.Context, src, partial string, compressed bool) error {
	r, closeInput, err := openArchiveInput(src, compressed)
	if err != nil {
		return err
	}
	defer closeInput()
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a dump's own temporary file
	if err != nil {
		return err
	}
	inputHash := sha256.New()
	if compressed {
		r = io.TeeReader(r, inputHash)
	}
	enc, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderConcurrency(1), zstd.WithZeroFrames(true))
	if err != nil {
		return errors.Join(err, f.Close())
	}
	_, copyErr := io.Copy(enc, contextReader{ctx, r})
	if err := errors.Join(copyErr, enc.Close(), f.Sync(), f.Close()); err != nil {
		return err
	}

	decoded, closeDecoded, err := openArchiveInput(partial, true)
	if err != nil {
		return err
	}
	defer closeDecoded()
	reader := contextReader{ctx, decoded}
	if !compressed {
		_, err := countCopyRows(reader)
		return err
	}
	outputHash := sha256.New()
	if _, err := io.Copy(outputHash, reader); err != nil {
		return err
	}
	if !bytes.Equal(inputHash.Sum(nil), outputHash.Sum(nil)) {
		return errors.New("recompressed table data differs from the original")
	}
	return nil
}
