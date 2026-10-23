package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

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
		return get(ctx, u, 4<<20)
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

// verifyFailure maps a releaseverify error to the run's failure code (the Updates page
// names the check that failed).
func verifyFailure(err error) error {
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
