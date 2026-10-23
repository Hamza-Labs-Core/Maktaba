package updater

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// RuntimeInstaller is the Linux app's runtime copy and a headless install's (Epic 39:
// the systemd user unit runs <runtime>/current/bin/maktaba-launcher): the release's
// signed Linux payload is installed as a new runtime directory, `current` moves to it
// (`previous` keeps the one that ran) and the supervisor exits 75; the launcher starts
// <runtime>/current again, and falls back to `previous` when it cannot stay up (its
// guard, <launcher root>/updates/pending.json).
type RuntimeInstaller struct {
	Runtime appruntime.Runtime
	Source  Source
	Version string
	// LauncherRoot is where the launcher keeps its lock and guard (MAKTABA_LAUNCHER_ROOT).
	LauncherRoot string
	// Record is the home's database record ("" on a node).
	Record string
	Now    func() time.Time
}

func (ri *RuntimeInstaller) now() time.Time {
	if ri.Now != nil {
		return ri.Now()
	}
	return time.Now()
}

// Kind implements Installer.
func (ri *RuntimeInstaller) Kind() string { return "runtime" }

// Running implements Installer.
func (ri *RuntimeInstaller) Running() string { return layout.NormalizeVersion(ri.Version) }

// Preflight implements Installer.
func (ri *RuntimeInstaller) Preflight(_ context.Context, _ *Journal) error {
	cur, err := ri.Runtime.Current()
	if err != nil || cur == "" {
		return Fail("no_runtime", "%s has no current runtime (%v)", ri.Runtime.Root, err)
	}
	probe := filepath.Join(ri.Runtime.Root, ".write-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return Fail("not_writable", "%s: %v", ri.Runtime.Root, err)
	}
	_ = os.Remove(probe)
	if ri.LauncherRoot == "" {
		return Fail("old_launcher", "the launcher that runs this server is from before automatic updates: it updates once by hand")
	}
	return nil
}

// Stage implements Installer: the payload tarball, verified against the signed
// checksums.txt, unpacked and installed as a runtime directory (current unchanged).
func (ri *RuntimeInstaller) Stage(ctx context.Context, j *Journal, _ io.Writer) error {
	res, err := ri.Source.verify(ctx, j)
	if err != nil {
		return err
	}
	asset := appruntime.ReleaseAsset(j.Job.Tag)
	u, ok := j.Job.Assets[asset]
	if !ok {
		return Fail("release_incomplete", "%s has no %s", j.Job.Tag, asset)
	}
	if err := os.MkdirAll(ri.Runtime.Root, 0o700); err != nil {
		return err
	}
	dl, err := os.MkdirTemp(ri.Runtime.Root, appruntime.DownloadPrefix)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dl) }()
	tarball := filepath.Join(dl, asset)
	if err := ri.Source.downloadTo(ctx, u, tarball, 2<<30); err != nil {
		return Fail("download_failed", "%s: %v", asset, err)
	}
	if err := res.VerifyArtifact(releaseverify.Artifact{Name: asset, Path: tarball}); err != nil {
		return verifyFailure(err)
	}
	payload, err := appruntime.Unpack(tarball, filepath.Join(dl, "payload"))
	if err != nil {
		return Fail("payload_invalid", "%v", err)
	}
	_ = os.Remove(tarball)
	p, err := appruntime.ReadPayload(payload)
	if err != nil {
		return Fail("payload_invalid", "%v", err)
	}
	if layout.CompareVersions(p.Version, j.Job.Version) != 0 {
		return Fail("version_mismatch", "%s holds the payload of %s, not of %s", asset, p.Version, j.Job.Version)
	}
	r, err := ri.Runtime.Install(ctx, payload, nil, appruntime.Options{})
	if err != nil {
		return Fail("install_failed", "%v", err)
	}
	cur, _ := ri.Runtime.Current()
	j.Staged, j.FromRef = filepath.Base(r.Dir), cur
	return nil
}

// Smoke implements Installer.
func (ri *RuntimeInstaller) Smoke(ctx context.Context, j *Journal) error {
	dir := filepath.Join(ri.Runtime.Root, j.Staged, "bin")
	for _, b := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming"} {
		if err := smokeRun(ctx, filepath.Join(dir, layout.Exe(b)), j.Job.Version); err != nil {
			return err
		}
	}
	return nil
}

// Tools implements Installer.
func (ri *RuntimeInstaller) Tools(j *Journal) (string, string) {
	dir := filepath.Join(ri.Runtime.Root, j.Staged)
	mig := filepath.Join(dir, "migrations")
	if st, err := os.Stat(mig); err != nil || !st.IsDir() {
		return "", ""
	}
	return filepath.Join(dir, "bin", layout.Exe("maktaba-api")), mig
}

// Switch implements Installer: the launcher's guard, then `current` to the new runtime.
func (ri *RuntimeInstaller) Switch(ctx context.Context, j *Journal) error {
	if err := layout.WriteGuard(ri.LauncherRoot, layout.Guard{Version: layout.NormalizeVersion(j.Job.Version),
		RunID: j.Job.RunID, Since: ri.now().UTC(), Deadline: guardDeadline(ri.now(), j).UTC(),
		PreviousRuntime: j.FromRef, Record: ri.Record}); err != nil {
		return Fail("switch_failed", "%v", err)
	}
	if _, err := ri.Runtime.Point(ctx, j.Staged, appruntime.Options{}); err != nil {
		_ = layout.ClearGuard(ri.LauncherRoot)
		return Fail("switch_failed", "%v", err)
	}
	return nil
}

// Restart implements Installer.
func (ri *RuntimeInstaller) Restart(_ context.Context, _ *Journal, exit func(int)) error {
	exit(75)
	return nil
}

// Revert implements Installer: `current` back to the runtime that ran before.
func (ri *RuntimeInstaller) Revert(ctx context.Context, j *Journal) error {
	if j.FromRef == "" {
		return Fail("no_previous", "no runtime ran before")
	}
	if _, err := ri.Runtime.Point(ctx, j.FromRef, appruntime.Options{AllowDowngrade: true}); err != nil {
		return err
	}
	return layout.ClearGuard(ri.LauncherRoot)
}

// Commit implements Installer.
func (ri *RuntimeInstaller) Commit(ctx context.Context, j *Journal) error {
	if err := layout.ClearGuard(ri.LauncherRoot); err != nil {
		return err
	}
	_ = ri.Runtime.RaiseWatermark(layout.NormalizeVersion(j.Job.Version))
	keep := j.Job.KeepVersions
	if keep < 2 {
		keep = 2
	}
	_, err := ri.Runtime.Prune(ctx, keep)
	return err
}

// Discard implements Installer.
func (ri *RuntimeInstaller) Discard(_ context.Context, j *Journal) error {
	if j.Staged == "" || j.Staged == "running" {
		return nil
	}
	cur, _ := ri.Runtime.Current()
	if j.Staged == cur || j.Staged == ri.Runtime.Previous() {
		return nil
	}
	return os.RemoveAll(filepath.Join(ri.Runtime.Root, j.Staged))
}
