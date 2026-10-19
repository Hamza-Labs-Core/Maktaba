package releaseverify

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// DirMode says what a directory holds.
type DirMode int

const (
	// ReleaseAssets: a directory of release assets as published (release.yml's
	// dist/): every file checksums.txt lists must be present and match.
	ReleaseAssets DirMode = iota
	// InstalledRelease: an extracted bin/releases/<v>/ directory (design §2.1):
	// the binaries of assets[platform].files must be present and match
	// release.json. This is the launcher's offline re-check.
	InstalledRelease
)

// VerifyDir verifies a directory holding checksums.txt, its Sigstore bundle and
// release.json, offline, against tm (nil: the embedded root).
func VerifyDir(dir, tag string, id Identity, mode DirMode, platform string, tm root.TrustedMaterial) (*Result, error) {
	read := func(name string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}
	in := Input{Tag: tag, Identity: id, TrustedMaterial: tm}
	var err error
	if in.Checksums, err = read(ChecksumsFile); err != nil || in.Checksums == nil {
		return nil, fail(ErrManifest, ChecksumsFile, "missing", err)
	}
	if in.Bundle, err = read(BundleFile); err != nil {
		return nil, fail(ErrSignature, BundleFile, "read", err)
	}
	if in.Manifest, err = read(ManifestFile); err != nil {
		return nil, fail(ErrManifest, ManifestFile, "read", err)
	}
	res, err := Verify(in)
	if err != nil {
		return nil, err
	}
	switch mode {
	case ReleaseAssets:
		for _, name := range res.Checksums.Names() {
			if err := res.VerifyArtifact(Artifact{Name: name, Path: filepath.Join(dir, name)}); err != nil {
				return nil, err
			}
		}
	case InstalledRelease:
		if platform == "" {
			platform = Platform()
		}
		a, ok := res.Manifest.Assets[platform]
		if !ok {
			return nil, fail(ErrManifest, ManifestFile, "no assets for "+platform, nil)
		}
		names := make([]string, 0, len(a.Files))
		for n := range a.Files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			b, err := os.ReadFile(filepath.Join(dir, n))
			if err != nil {
				return nil, fail(ErrChecksum, n, "missing", err)
			}
			if err := res.Manifest.VerifyExtracted(platform, n, b); err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}
