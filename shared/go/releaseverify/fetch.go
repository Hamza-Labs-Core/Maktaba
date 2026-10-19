package releaseverify

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"runtime"
	"strings"
)

// Size caps for downloads: the three small release files and an archive.
const (
	maxSmallFile = 4 << 20
	maxArchive   = 512 << 20
	maxBinary    = 512 << 20
)

// Platform is this machine's release.json assets key ("linux/amd64").
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// VerifyArtifact checks one more file of a verified release against
// checksums.txt (used when the file to install is only known from release.json).
func (r *Result) VerifyArtifact(a Artifact) error { return checkArtifact(a, r.Checksums, r.Manifest) }

// Downloader fetches a URL with a size cap.
type Downloader func(ctx context.Context, url string, limit int64) ([]byte, error)

// HTTPDownloader is a Downloader over c (nil: http.DefaultClient).
func HTTPDownloader(c *http.Client) Downloader {
	if c == nil {
		c = http.DefaultClient
	}
	return func(ctx context.Context, url string, limit int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("GET %s: larger than %d bytes", url, limit)
		}
		return b, nil
	}
}

// ErrNotFound is returned (wrapped) by FetchServer when the release lacks a file.
var ErrNotFound = errors.New("release file not found")

// ServerUpdate is a verified server archive for this platform with the
// maktaba-server binary taken out of it and checked against release.json.
type ServerUpdate struct {
	Result  *Result
	Asset   Asset
	Archive []byte
	Binary  []byte // the maktaba-server executable
	Entry   string // its name in the archive (maktaba-server[.exe])
}

// FetchServer is the whole download-and-verify path of a server self-update
// (the API's POST /api/admin/system/update and `maktaba-server update`): it
// fetches checksums.txt, its Sigstore bundle and release.json from the release's
// assets, runs Verify, then fetches this platform's archive named by release.json,
// checks it against checksums.txt and the extracted maktaba-server against
// release.json. Nothing is written to disk.
func FetchServer(ctx context.Context, get Downloader, in Input, assets map[string]string, platform string) (*ServerUpdate, error) {
	if platform == "" {
		platform = Platform()
	}
	small := func(name string) ([]byte, error) {
		u, ok := assets[name]
		if !ok {
			return nil, nil
		}
		b, err := get(ctx, u, maxSmallFile)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", name, err)
		}
		return b, nil
	}
	var err error
	if in.Checksums, err = small(ChecksumsFile); err != nil {
		return nil, err
	}
	if in.Checksums == nil {
		return nil, fail(ErrManifest, ChecksumsFile, "release has no checksums.txt", ErrNotFound)
	}
	if in.Bundle, err = small(BundleFile); err != nil {
		return nil, err
	}
	if in.Manifest, err = small(ManifestFile); err != nil {
		return nil, err
	}
	in.Artifacts = nil
	res, err := Verify(in)
	if err != nil {
		return nil, err
	}
	asset, ok := res.Manifest.Assets[platform]
	if !ok {
		return nil, fail(ErrManifest, ManifestFile, "no server archive for "+platform, ErrNotFound)
	}
	u, ok := assets[asset.Archive]
	if !ok {
		return nil, fail(ErrManifest, asset.Archive, "listed in release.json but not attached to the release", ErrNotFound)
	}
	archive, err := get(ctx, u, maxArchive)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Archive, err)
	}
	if err := res.VerifyArtifact(Artifact{Name: asset.Archive, Data: archive}); err != nil {
		return nil, err
	}
	entry, bin, err := ExtractBinary(asset.Archive, archive, "maktaba-server")
	if err != nil {
		return nil, fail(ErrManifest, asset.Archive, "", err)
	}
	if err := res.Manifest.VerifyExtracted(platform, entry, bin); err != nil {
		return nil, err
	}
	return &ServerUpdate{Result: res, Asset: asset, Archive: archive, Binary: bin, Entry: entry}, nil
}

// ExtractBinary returns the regular file named base (or base+".exe") from a
// .tar.gz or .zip release archive, wherever it sits in the archive.
func ExtractBinary(archiveName string, archive []byte, base string) (string, []byte, error) {
	match := func(name string) bool {
		b := path.Base(strings.ReplaceAll(name, "\\", "/"))
		return b == base || b == base+".exe"
	}
	switch {
	case strings.HasSuffix(archiveName, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return "", nil, err
		}
		for _, f := range zr.File {
			if !match(f.Name) || f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return "", nil, err
			}
			b, err := readCapped(rc, maxBinary)
			_ = rc.Close()
			return path.Base(f.Name), b, err
		}
	case strings.HasSuffix(archiveName, ".tar.gz"), strings.HasSuffix(archiveName, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return "", nil, err
		}
		defer func() { _ = gz.Close() }()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", nil, err
			}
			if hdr.Typeflag == tar.TypeReg && match(hdr.Name) {
				b, err := readCapped(tr, maxBinary)
				return path.Base(hdr.Name), b, err
			}
		}
	default:
		return "", nil, fmt.Errorf("unsupported archive %s", archiveName)
	}
	return "", nil, fmt.Errorf("%s not found in %s", base, archiveName)
}

func readCapped(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("entry larger than %d bytes", limit)
	}
	return b, nil
}
