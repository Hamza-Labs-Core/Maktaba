// Package install puts a verified release into the side-by-side layout (Epic
// 35, story 35.2 design §2.1) — the one install flow `maktaba-server update`,
// the API's self-update and the update orchestrator share:
//
//  1. releaseverify.FetchServer: checksums.txt, its Sigstore bundle and
//     release.json verified against the pinned identity, then this platform's
//     archive against checksums.txt (story 35.1);
//  2. the watermark rules (<install>/watermark, never a downgrade or a replay);
//  3. every binary release.json lists for the platform extracted into
//     bin/releases/.staging-<v>-*/ next to checksums.txt, the bundle and
//     release.json, the directory re-verified offline (VerifyDir,
//     InstalledRelease), .verified written, renamed to bin/releases/<v>/;
//  4. Activate: current = <v> under the launcher's boot-loop guard (pending),
//     the watermark committed. The launcher starts it on the next restart
//     (exit 75) and falls back to previous if it does not come up.
package install

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// small-file download limit (checksums.txt, bundle, release.json)
const maxSmall = 4 << 20

// WatermarkPath is the layout's watermark: one line, "<v>\n" (design §2.1),
// with releaseverify's full record (checksums digest, signed time) beside it
// in watermark.json.
func WatermarkPath(l layout.Layout) string { return filepath.Join(l.Root, layout.WatermarkFile) }

// Watermark is the layout's releaseverify watermark file.
func Watermark(l layout.Layout) releaseverify.WatermarkFile {
	return releaseverify.WatermarkFile{Path: WatermarkPath(l)}
}

// Staged is a verified release under bin/releases/<v>/, not yet current.
type Staged struct {
	Version string
	Dir     string
	Result  *releaseverify.Result
}

// Root finds the layout an executable runs from: <install>/bin/releases/<v>/
// <exe> → <install>; else $MAKTABA_INSTALL_ROOT when it is a layout. ok=false
// for a flat (pre-35.2) install.
func Root(exe string) (layout.Layout, bool) {
	dir := filepath.Dir(filepath.Clean(exe))
	rel := filepath.Dir(dir)
	if filepath.Base(rel) == "releases" && filepath.Base(filepath.Dir(rel)) == "bin" {
		root := filepath.Dir(filepath.Dir(rel))
		if layout.IsLayout(root) {
			return layout.New(root), true
		}
	}
	if r := os.Getenv(layout.EnvInstallRoot); r != "" && layout.IsLayout(r) {
		return layout.New(r), true
	}
	return layout.Layout{}, false
}

// Stage downloads, verifies and stages a release; nothing outside
// bin/releases/ changes. A release already staged and valid is re-used.
func Stage(ctx context.Context, l layout.Layout, get releaseverify.Downloader, in releaseverify.Input,
	assets map[string]string, platform string) (*Staged, error) {
	if platform == "" {
		platform = releaseverify.Platform()
	}
	up, err := releaseverify.FetchServer(ctx, get, in, assets, platform)
	if err != nil {
		return nil, err
	}
	w, err := Watermark(l).Load()
	if err != nil {
		return nil, fmt.Errorf("read watermark: %w", err)
	}
	if err := releaseverify.CheckMonotonic(w, up.Result); err != nil {
		return nil, err
	}
	v := up.Result.Manifest.Version
	if !layout.ValidVersion(v) {
		return nil, fmt.Errorf("%w: %q", layout.ErrBadVersion, v)
	}
	final := l.ReleaseDir(v)
	st := &Staged{Version: v, Dir: final, Result: up.Result}
	if err := l.ValidateRelease(v); err == nil {
		if _, err := releaseverify.VerifyDir(final, in.Tag, in.Identity, releaseverify.InstalledRelease, platform, in.TrustedMaterial); err == nil {
			return st, nil
		}
	}

	if err := os.MkdirAll(l.ReleasesDir(), 0o755); err != nil {
		return nil, err
	}
	tmp := filepath.Join(l.ReleasesDir(), ".staging-"+v+"-"+randHex())
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp, 0o755); err != nil { // the launcher refuses a shared-writable release
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// the signed files, as published (VerifyDir re-checks them offline)
	for _, name := range []string{releaseverify.ChecksumsFile, releaseverify.BundleFile, releaseverify.ManifestFile} {
		u, ok := assets[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", releaseverify.ErrNotFound, name)
		}
		b, err := get(ctx, u, maxSmall)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			return nil, err
		}
	}
	// every binary of the platform (server, api, streaming, launcher)
	names := make([]string, 0, len(up.Asset.Files))
	for n := range up.Asset.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if strings.ContainsAny(n, `/\`) || n == "." || n == ".." {
			return nil, fmt.Errorf("release.json lists a file with a path: %q", n)
		}
		_, b, err := releaseverify.ExtractBinary(up.Asset.Archive, up.Archive, strings.TrimSuffix(n, ".exe"))
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", n, err)
		}
		if err := up.Result.Manifest.VerifyExtracted(platform, n, b); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(tmp, n), b, 0o755); err != nil {
			return nil, err
		}
		if err := os.Chmod(filepath.Join(tmp, n), 0o755); err != nil { // whatever the umask
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, layout.Exe("maktaba-server"))); err != nil {
		return nil, fmt.Errorf("the %s archive has no %s", platform, layout.Exe("maktaba-server"))
	}
	if _, err := releaseverify.VerifyDir(tmp, in.Tag, in.Identity, releaseverify.InstalledRelease, platform, in.TrustedMaterial); err != nil {
		return nil, err
	}
	if _, err := layout.WriteVerified(tmp, up.Result.Identity, v, time.Now()); err != nil {
		return nil, err
	}
	if _, err := os.Stat(final); err == nil { // present but not valid
		aside := filepath.Join(l.ReleasesDir(), ".broken-"+v+"-"+randHex())
		if err := os.Rename(final, aside); err != nil {
			return nil, fmt.Errorf("move the invalid %s aside: %w", final, err)
		}
		_ = os.RemoveAll(aside)
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	return st, nil
}

// Activate makes a staged release current under the launcher's boot-loop
// guard and commits the watermark. runID names the run in the pending marker.
func (s *Staged) Activate(l layout.Layout, runID string) error {
	if err := l.Switch(s.Version, runID, time.Now()); err != nil {
		return err
	}
	return Watermark(l).Commit(s.Result)
}

// ErrNoPrevious: a rollback without a kept previous release.
var ErrNoPrevious = errors.New("no previous release kept")

// Rollback makes the previous release current again (the only way to run
// below the watermark, which it does not lower). It returns that version.
func Rollback(l layout.Layout) (string, error) {
	v, err := l.Rollback()
	if errors.Is(err, layout.ErrNoPointer) {
		return "", ErrNoPrevious
	}
	return v, err
}

func randHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
