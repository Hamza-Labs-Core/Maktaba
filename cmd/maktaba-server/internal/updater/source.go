package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Source is where a release comes from and what it is checked against: the pinned
// repository's signing identity and the Sigstore trusted root. Every machine verifies
// what it installs itself; the home's word is never the trust anchor.
type Source struct {
	Identity releaseverify.Identity
	// Trusted returns the trusted root (a RootStore refreshed through TUF).
	Trusted func(ctx context.Context) (root.TrustedMaterial, error)
	HTTP    *http.Client
	// Platform is release.json's os/arch ("" this machine's).
	Platform string
	// Wheels fetches the home's wheelhouse of a release for this platform into dir (a
	// node's: homemirror); false when the home keeps none. Nil on the home and on a
	// machine of no home: the pipeline's packages come from PyPI.
	Wheels func(ctx context.Context, version, platform, dir string) (bool, error)
}

// wheelhouse fetches the home's wheelhouse of j's release into dir and says whether it
// is there (a failure is logged: the environment's packages come from PyPI then).
func (s Source) wheelhouse(ctx context.Context, j *Journal, dir string, log io.Writer) bool {
	if s.Wheels == nil {
		return false
	}
	ok, err := s.Wheels(ctx, j.Job.Version, s.platform(), dir)
	if err != nil && log != nil {
		_, _ = fmt.Fprintf(log, "[update] the home's wheelhouse was not fetched (%v): the pipeline's packages come from PyPI\n", err)
	}
	return ok && err == nil
}

// WheelhouseDir is where a desktop app's or a runtime copy's release keeps the home's
// wheelhouse it was staged with, in root (<updates>/wheelhouse): the app's venv step builds
// the pipeline's environment from it at the release's first start.
func WheelhouseDir(root, version string) string {
	return filepath.Join(root, safeName(layout.NormalizeVersion(version)))
}

// stageWheels fetches the home's wheelhouse of j's release into root's folder for it,
// after removing the folders of every other release (a node's; nothing without Wheels).
func (s Source) stageWheels(ctx context.Context, j *Journal, root string, log io.Writer) {
	if s.Wheels == nil || root == "" {
		return
	}
	keep := filepath.Base(WheelhouseDir(root, j.Job.Version))
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.Name() != keep {
				_ = os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	s.wheelhouse(ctx, j, WheelhouseDir(root, j.Job.Version), log)
}

func (s Source) get() releaseverify.Downloader {
	c := s.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Minute}
	}
	return releaseverify.HTTPDownloader(c)
}

func (s Source) platform() string {
	if s.Platform != "" {
		return s.Platform
	}
	return releaseverify.Platform()
}

// input is the verification input for tag.
func (s Source) input(ctx context.Context, tag string) (releaseverify.Input, error) {
	if s.Trusted == nil {
		return releaseverify.Input{}, Fail("no_trust_root", "this build cannot verify releases")
	}
	tm, err := s.Trusted(ctx)
	if err != nil {
		return releaseverify.Input{}, Fail("trust_root", "%v", err)
	}
	return releaseverify.Input{Tag: tag, Identity: s.Identity, TrustedMaterial: tm}, nil
}

// verify fetches the release's signed files and verifies them (nothing else).
func (s Source) verify(ctx context.Context, j *Journal) (*releaseverify.Result, error) {
	in, err := s.input(ctx, j.Job.Tag)
	if err != nil {
		return nil, err
	}
	get := s.get()
	small := func(name string) ([]byte, error) {
		u, ok := j.Job.Assets[name]
		if !ok {
			return nil, Fail("release_incomplete", "%s has no %s", j.Job.Tag, name)
		}
		b, err := get(ctx, u, 4<<20)
		if err != nil {
			return nil, Fail("download_failed", "%s: %v", name, err)
		}
		return b, nil
	}
	if in.Checksums, err = small(releaseverify.ChecksumsFile); err != nil {
		return nil, err
	}
	if in.Bundle, err = small(releaseverify.BundleFile); err != nil {
		return nil, err
	}
	if in.Manifest, err = small(releaseverify.ManifestFile); err != nil {
		return nil, err
	}
	res, err := releaseverify.Verify(in)
	if err != nil {
		return nil, verifyFailure(err)
	}
	if layout.CompareVersions(res.Manifest.Version, j.Job.Version) != 0 {
		return nil, Fail("version_mismatch", "release.json is %s, the home asked for %s", res.Manifest.Version, j.Job.Version)
	}
	return res, nil
}

// freeDisk is hostinfo.FreeDisk (tests replace it).
var freeDisk = hostinfo.FreeDisk

// Room a release needs on its disk: its programs (with the archive beside them while
// they are unpacked), and on a plain server install its pipeline environment too.
const (
	roomRelease = 2 << 30
	roomVenv    = 6 << 30
)

// checkRoom fails the preflight (disk_space) when the disk holding dir has less than
// need free (an unknown free space passes).
func checkRoom(dir string, need uint64) error {
	if have := freeDisk(dir); have > 0 && have < need {
		return Fail("disk_space", "%s has %.1f GiB free; the release needs %.1f GiB: free some space and the update goes on",
			dir, float64(have)/(1<<30), float64(need)/(1<<30))
	}
	return nil
}

// verifyFailure maps a release check's error to the run's failure code (the Updates page
// names the check that failed). Only releaseverify's own failures are verify_* (a release
// that is not what it says: the home blocks it); anything else (a download's HTTP error
// or timeout, a full or unwritable disk while it is fetched) is download_failed, an attempt
// tried again; an older build than the release installed here (releaseverify.OlderBuild:
// an rc tagged after its release) is older_build, passed by.
func verifyFailure(err error) error {
	var f *Failure
	if errors.As(err, &f) {
		return err // classified already
	}
	var rv *releaseverify.Error
	if !errors.As(err, &rv) {
		return &Failure{Code: "download_failed", Detail: err.Error()}
	}
	if isErr(err, releaseverify.ErrOlderBuild) {
		return &Failure{Code: "older_build", Detail: err.Error()}
	}
	code := "verify_failed"
	for _, c := range []struct {
		e    error
		code string
	}{{releaseverify.ErrSignature, "signature"}, {releaseverify.ErrIdentity, "identity"},
		{releaseverify.ErrTlog, "transparency_log"}, {releaseverify.ErrChecksum, "checksum"},
		{releaseverify.ErrManifest, "manifest"}, {releaseverify.ErrNoHashes, "unhashed_dependencies"},
		{releaseverify.ErrRootExpired, "trust_root_expired"}, {releaseverify.ErrReplay, "replay"},
		{releaseverify.ErrDowngrade, "downgrade"}} {
		if isErr(err, c.e) {
			code = "verify_" + c.code
			break
		}
	}
	return &Failure{Code: code, Detail: err.Error()}
}

// smokeRun runs bin --version and checks it names version (30 s at most).
func smokeRun(ctx context.Context, bin, version string) error {
	if _, err := os.Stat(bin); err != nil {
		return Fail("smoke_missing", "%s: %v", bin, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return Fail("smoke_failed", "%s --version: %v: %s", bin, err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), layout.NormalizeVersion(version)) {
		return Fail("smoke_version", "%s says %q, not %s", bin, strings.TrimSpace(string(out)), version)
	}
	return nil
}

// copyFile copies src to dst with mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// downloadTo streams url into a new file at path (at most limit bytes).
func (s Source) downloadTo(ctx context.Context, url, path string, limit int64) error {
	c := s.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = errf("GET %s: larger than %d bytes", url, limit)
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
