package updater

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
)

func isErr(err, target error) bool { return errors.Is(err, target) }

// LayoutInstaller is a plain server install over the side-by-side layout (story 35.2:
// the launcher runs bin/releases/<current>/maktaba-server): the release is staged in
// bin/releases/<v> with its migrations, switched to under the launcher's guard
// (pending), and the supervisor exits 75 for the launcher to start it.
type LayoutInstaller struct {
	Layout layout.Layout
	Source Source
	// Version is the running release (the current release directory's).
	Version string
	// Record is the home's database record (state/database.json) the launcher's guard
	// marks on a fallback ("" on a node).
	Record string
	// PipelinePython is the configured [pipeline] python ("" none): the interpreter its
	// environment was made from builds the release's.
	PipelinePython string
	Now            func() time.Time
}

func (li *LayoutInstaller) now() time.Time {
	if li.Now != nil {
		return li.Now()
	}
	return time.Now()
}

// Kind implements Installer.
func (li *LayoutInstaller) Kind() string { return "layout" }

// Running implements Installer.
func (li *LayoutInstaller) Running() string { return layout.NormalizeVersion(li.Version) }

// Preflight implements Installer: the layout is writable and its running release kept.
func (li *LayoutInstaller) Preflight(_ context.Context, _ *Journal) error {
	if err := os.MkdirAll(li.Layout.UpdatesDir(), 0o755); err != nil {
		return Fail("not_writable", "%v", err)
	}
	if err := checkRoom(li.Layout.ReleasesDir(), roomRelease+roomVenv); err != nil {
		return err
	}
	probe := filepath.Join(li.Layout.ReleasesDir(), ".write-probe")
	if err := os.MkdirAll(li.Layout.ReleasesDir(), 0o755); err != nil {
		return Fail("not_writable", "%v", err)
	}
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return Fail("not_writable", "%s: %v", li.Layout.ReleasesDir(), err)
	}
	_ = os.Remove(probe)
	if err := li.Layout.ValidateRelease(li.Running()); err != nil {
		return Fail("no_previous", "the running release %s is not kept in the layout (%v): it could not be gone back to",
			li.Running(), err)
	}
	return nil
}

// Stage implements Installer: install.Stage (download, verify, extract, re-verify, the
// migrations beside the binaries, .verified).
func (li *LayoutInstaller) Stage(ctx context.Context, j *Journal, log io.Writer) error {
	in, err := li.Source.input(ctx, j.Job.Tag)
	if err != nil {
		return err
	}
	var st *install.Staged
	if err := retryFetch(ctx, func() error {
		var err error
		if st, err = install.Stage(ctx, li.Layout, li.Source.get(), in, j.Job.Assets, li.Source.platform()); err != nil {
			return verifyFailure(err)
		}
		return nil
	}); err != nil {
		return err
	}
	if layout.CompareVersions(st.Version, j.Job.Version) != 0 {
		return Fail("version_mismatch", "release.json is %s, the home asked for %s", st.Version, j.Job.Version)
	}
	j.Staged, j.FromRef = st.Dir, li.Running()
	if st.Result == nil {
		return nil
	}
	j.LauncherProtocol = st.Result.Manifest.LauncherProtocol
	ok, why := li.launcherReplaceable(j.Job.Version)
	if err := checkLauncher(j, ok, why); err != nil {
		return err
	}
	return li.buildVenv(ctx, j, st.Result, st.Dir, log)
}

// Smoke implements Installer.
func (li *LayoutInstaller) Smoke(ctx context.Context, j *Journal) error {
	for _, b := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming"} {
		if err := smokeRun(ctx, filepath.Join(j.Staged, layout.Exe(b)), j.Job.Version); err != nil {
			return err
		}
	}
	return smokePipeline(ctx, j.Staged, j.Job.Version)
}

// Tools implements Installer.
func (li *LayoutInstaller) Tools(j *Journal) (string, string) {
	mig := filepath.Join(j.Staged, install.MigrationsDir)
	if st, err := os.Stat(mig); err != nil || !st.IsDir() {
		return "", ""
	}
	return filepath.Join(j.Staged, layout.Exe("maktaba-api")), mig
}

// guardDeadline is how long the launcher waits for the new release to confirm: its
// database step (the rehearsal measured the migrations), the watch, and room.
func guardDeadline(now time.Time, j *Journal) time.Time {
	d := j.Job.Watch() + j.Job.HealthLimit() + 20*time.Minute
	if j.Rehearsal != nil && j.Rehearsal.MigrateSec > 0 {
		d += 3 * time.Duration(j.Rehearsal.MigrateSec*float64(time.Second))
	}
	return now.Add(d)
}

// Switch implements Installer: current = <v> under the launcher's guard.
func (li *LayoutInstaller) Switch(_ context.Context, j *Journal) error {
	v := layout.NormalizeVersion(j.Job.Version)
	if err := li.Layout.Switch(v, j.Job.RunID, li.now()); err != nil {
		return Fail("switch_failed", "%v", err)
	}
	if err := li.Layout.GuardPending(v, guardDeadline(li.now(), j), li.Record); err != nil {
		return Fail("switch_failed", "%v", err)
	}
	return nil
}

// Extend implements Extender: the pending marker's deadline.
func (li *LayoutInstaller) Extend(j *Journal, until time.Time) error {
	_, err := li.Layout.ExtendPending(j.Job.RunID, until)
	return err
}

// Restart implements Installer: exit 75 (the launcher starts current).
func (li *LayoutInstaller) Restart(_ context.Context, _ *Journal, exit func(int)) error {
	exit(75)
	return nil
}

// Revert implements Installer: current = the release that ran before.
func (li *LayoutInstaller) Revert(_ context.Context, j *Journal) error {
	from := layout.NormalizeVersion(j.From)
	if from == "" {
		return Fail("no_previous", "no release ran before")
	}
	if cur, _ := li.Layout.Current(); cur == from {
		return li.Layout.ClearPending()
	}
	if err := li.Layout.ValidateRelease(from); err != nil {
		return Fail("no_previous", "%s cannot be started again: %v", from, err)
	}
	if err := li.Layout.WritePointer(layout.CurrentFile, from); err != nil {
		return err
	}
	return li.Layout.ClearPending()
}

// Commit implements Installer: confirm (pending gone), the watermark with the signed
// release's record (replays refused from now on), prune.
func (li *LayoutInstaller) Commit(ctx context.Context, j *Journal) error {
	v := layout.NormalizeVersion(j.Job.Version)
	if err := li.Layout.Confirm(); err != nil {
		return err
	}
	_ = li.Layout.RaiseWatermark(v)
	if in, err := li.Source.input(ctx, j.Job.Tag); err == nil {
		if res, err := releaseverify.VerifyDir(li.Layout.ReleaseDir(v), j.Job.Tag, in.Identity,
			releaseverify.InstalledRelease, li.Source.platform(), in.TrustedMaterial); err == nil {
			_ = install.Watermark(li.Layout).Commit(res)
		}
	}
	keep := j.Job.KeepVersions
	if keep < 2 {
		keep = 3
	}
	// what a prune leaves is pruned by the next commit; the release is kept either way
	_, _ = li.Layout.Prune(keep, layout.NormalizeVersion(j.From))
	return nil
}

// Discard implements Installer: the staged release goes unless something runs it.
func (li *LayoutInstaller) Discard(_ context.Context, j *Journal) error {
	if j.Staged == "" || j.Staged == "running" {
		return nil
	}
	v := filepath.Base(j.Staged)
	cur, _ := li.Layout.Current()
	prev, _ := li.Layout.Previous()
	if v == cur || v == prev || v == li.Running() {
		return nil
	}
	return os.RemoveAll(j.Staged)
}
