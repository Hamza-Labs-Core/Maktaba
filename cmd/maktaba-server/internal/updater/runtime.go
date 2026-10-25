package updater

import (
	"context"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// RuntimeInstaller is the Linux app's runtime copy and a headless install's (Epic 39:
// the systemd user unit runs <runtime>/current/bin/maktaba-launcher): the release's
// signed Linux payload is installed as a new runtime directory, `current` moves to it
// (`previous` keeps the one that ran) and the supervisor exits 75; the launcher starts
// <runtime>/current again, and falls back to `previous` when it cannot stay up (its
// guard, <launcher root>/updates/pending.json). The Linux app's runtime switches only
// once someone clicks in the app (owner decision D1, request.go); a headless one by
// itself (D3: the server updates its runtime either way, no root needed).
type RuntimeInstaller struct {
	Runtime appruntime.Runtime
	Source  Source
	Version string
	// LauncherRoot is where the launcher keeps its lock and guard (MAKTABA_LAUNCHER_ROOT);
	// the order to the app is in its updates directory (the app's state folder).
	LauncherRoot string
	// Owner is who owns the runtime (MAKTABA_RUNTIME_OWNER): "app" asks, "headless" not.
	Owner string
	// Record is the home's database record ("" on a node).
	Record string
	// Open starts the Linux app with the order's deep link when it does not answer
	// within NudgeAfter (90 s); tests replace it.
	Open       func(ctx context.Context, url string) error
	NudgeAfter time.Duration
	Now        func() time.Time
	// StopPython stops this machine's Python processes (the pipeline) before `current`
	// moves to a runtime of another Python minor: they would load the new runtime's files
	// by path while they drain (nil: nothing stopped).
	StopPython func(ctx context.Context) error
	// StartPython turns the Python processes back on when the switch goes no further after
	// StopPython (the guard or the pointer could not be written): the release before keeps
	// running, its pipeline with it (nil: nothing to turn on).
	StartPython func(ctx context.Context) error
	// WheelRoot is where the home's wheelhouse of a staged release goes (WheelhouseDir):
	// the app's venv step builds the pipeline's environment from it ("" none).
	WheelRoot string

	nudge nudger
}

func (ri *RuntimeInstaller) requests() requests {
	return requests{dir: filepath.Join(ri.LauncherRoot, "updates"), now: ri.now}
}

// Asks implements Asker: the Linux app's runtime does.
func (ri *RuntimeInstaller) Asks() bool { return ri.Owner == "app" && ri.LauncherRoot != "" }

// Ask implements Asker: the order (nothing for the app to fetch).
func (ri *RuntimeInstaller) Ask(j *Journal) error {
	return ri.requests().order(j, RequestKindRuntime, "")
}

// Answer implements Asker (the app started when it does not answer).
func (ri *RuntimeInstaller) Answer(j *Journal) (string, error) {
	state, err := ri.requests().answer(j)
	if err == nil && state == RequestOrdered {
		after := ri.NudgeAfter
		if after <= 0 {
			after = 90 * time.Second
		}
		ri.nudge.nudge(ri.requests(), j, ri.now(), after, ri.Open)
	}
	return state, err
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
func (ri *RuntimeInstaller) Preflight(_ context.Context, j *Journal) error {
	cur, err := ri.Runtime.Current()
	if err != nil || cur == "" {
		return Fail("no_runtime", "%s has no current runtime (%v)", ri.Runtime.Root, err)
	}
	if err := checkRoom(ri.Runtime.Root, roomRelease); err != nil {
		return err
	}
	probe := filepath.Join(ri.Runtime.Root, ".write-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return Fail("not_writable", "%s: %v", ri.Runtime.Root, err)
	}
	_ = os.Remove(probe)
	if ri.LauncherRoot == "" {
		return Fail("old_launcher", "the launcher that runs this server is from before automatic updates: it updates once by hand")
	}
	// a release whose PostgreSQL would not open this machine's database: refused before
	// anything is downloaded (the job carries the release's word)
	return jobPGMajorChange(ri.payloadOf(""), j.Job.AppPostgresMajor, j.Job.Version)
}

// Stage implements Installer: the payload tarball, verified against the signed
// checksums.txt, unpacked and installed as a runtime directory (current unchanged).
func (ri *RuntimeInstaller) Stage(ctx context.Context, j *Journal, log io.Writer) error {
	var res *releaseverify.Result
	if err := retryFetch(ctx, func() error {
		var err error
		res, err = ri.Source.verify(ctx, j)
		return err
	}); err != nil {
		return err
	}
	// the one "never go back" rule against the release this runtime committed last
	if err := checkNotBack(ri.Runtime.Root, ri.Running(), res); err != nil {
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
	if err := retryFetch(ctx, func() error {
		if err := ri.Source.downloadTo(ctx, u, tarball, 2<<30); err != nil {
			return Fail("download_failed", "%s: %v", asset, err)
		}
		if err := res.VerifyArtifact(releaseverify.Artifact{Name: asset, Path: tarball}); err != nil {
			return verifyFailure(err)
		}
		return nil
	}); err != nil {
		return err
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
	// a release whose Postgres is another major than the one this machine's database was
	// made by needs an upgrade between the two, which no update does: refused before it is
	// installed (a person's: the release never starts on the old data directory)
	if err := pgMajorChange(ri.payloadOf(""), payloadAt(payload), j.Job.Version); err != nil {
		return err
	}
	j.LauncherProtocol = res.Manifest.LauncherProtocol // the new runtime brings its launcher
	r, err := ri.Runtime.Install(ctx, payload, nil, appruntime.Options{})
	if err != nil {
		return Fail("install_failed", "%v", err)
	}
	cur, _ := ri.Runtime.Current()
	j.Staged, j.FromRef, j.Signed = filepath.Base(r.Dir), cur, signedOf(res)
	ri.Source.stageWheels(ctx, j, ri.WheelRoot, log)
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

// payloadOf is the payload.json of the runtime directory name ("": current); empty when
// it cannot be read.
func (ri *RuntimeInstaller) payloadOf(name string) apppayload.Payload {
	if name == "" {
		name, _ = ri.Runtime.Current()
	}
	if name == "" {
		return apppayload.Payload{}
	}
	return payloadAt(filepath.Join(ri.Runtime.Root, filepath.Base(name)))
}

// payloadAt is dir's payload.json (empty when it cannot be read).
func payloadAt(dir string) apppayload.Payload {
	p, _ := apppayload.Read(filepath.Join(dir, "payload.json"))
	return p
}

// pgMajorChange refuses a release whose bundled Postgres is another major than the
// running payload's (pg_major_change, a person's): its data directory would not open,
// and no update upgrades it yet. Unknown majors decide nothing.
func pgMajorChange(have, want apppayload.Payload, version string) error {
	h, w := have.PostgresMajor(), want.PostgresMajor()
	if h == "" || w == "" || h == w {
		return nil
	}
	return Fail("pg_major_change", "%s carries PostgreSQL %s and this machine's database was made by PostgreSQL %s: "+
		"the database needs an upgrade between the two, which no update does yet", version, w, h)
}

// appPayloadKind is this machine's app payload as release.json names it.
func appPayloadKind() string {
	switch goruntime.GOOS {
	case "darwin":
		return "mac-app"
	case "windows":
		return "win-app"
	}
	return "linux-app"
}

// jobPGMajorChange is pgMajorChange from the job's word (the home's verified catalog:
// release.json's app_postgres_major for this platform's app payload), in preflight,
// before anything is downloaded. A job that does not say decides nothing.
func jobPGMajorChange(have apppayload.Payload, majors map[string]int, version string) error {
	want := majors[appPayloadKind()]
	if want == 0 {
		return nil
	}
	var p apppayload.Payload
	p.Postgres.Version = strconv.Itoa(want)
	return pgMajorChange(have, p, version)
}

// appPGMajorChange is pgMajorChange for an app bundle, before the app installs it: the
// running payload's Postgres against the one the release's signed release.json says this
// platform's app bundles (pg_major_change, a person's). A release that does not say, or a
// payload that does not, decides nothing (the start's database step still halts).
func appPGMajorChange(have apppayload.Payload, res *releaseverify.Result, version string) error {
	if res == nil {
		return nil
	}
	want := res.Manifest.PostgresMajor(appPayloadKind())
	if want == 0 {
		return nil
	}
	var p apppayload.Payload
	p.Postgres.Version = strconv.Itoa(want)
	return pgMajorChange(have, p, version)
}

// pythonMinor is "3.12" of "3.12.7" ("" when not known).
func pythonMinor(v string) string {
	parts := strings.SplitN(strings.TrimSpace(v), ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// Switch implements Installer: the launcher's guard, then `current` to the new runtime.
// When the new runtime's Python is another minor, the Python processes stop first (they
// would load the new runtime's files by path while they drain).
func (ri *RuntimeInstaller) Switch(ctx context.Context, j *Journal) error {
	stopped := false
	// the pipeline back on when the switch goes no further (the release before runs on)
	backOn := func() {
		if stopped && ri.StartPython != nil {
			_ = ri.StartPython(context.Background())
		}
	}
	if ri.StopPython != nil {
		have, want := pythonMinor(ri.payloadOf(j.FromRef).Python.Version), pythonMinor(ri.payloadOf(j.Staged).Python.Version)
		if have != "" && want != "" && have != want {
			stopped = true // even a stop that failed may have stopped some
			if err := ri.StopPython(ctx); err != nil {
				backOn()
				return Fail("switch_failed", "stopping the pipeline before its Python changes (%s to %s): %v", have, want, err)
			}
		}
	}
	if err := layout.WriteGuard(ri.LauncherRoot, layout.Guard{Version: layout.NormalizeVersion(j.Job.Version),
		RunID: j.Job.RunID, Since: ri.now().UTC(), Deadline: guardDeadline(ri.now(), j).UTC(),
		PreviousRuntime: j.FromRef, Record: ri.Record}); err != nil {
		backOn()
		return Fail("switch_failed", "%v", err)
	}
	if _, err := ri.Runtime.Point(ctx, j.Staged, appruntime.Options{}); err != nil {
		_ = layout.ClearGuard(ri.LauncherRoot)
		backOn()
		return Fail("switch_failed", "%v", err)
	}
	return nil
}

// Extend implements Extender: the launcher's guard deadline.
func (ri *RuntimeInstaller) Extend(j *Journal, until time.Time) error {
	_, err := layout.ExtendGuard(ri.LauncherRoot, j.Job.RunID, until)
	return err
}

// Restart implements Installer (the app's order, when there is one, says installing).
func (ri *RuntimeInstaller) Restart(_ context.Context, j *Journal, exit func(int)) error {
	if ri.Asks() {
		_ = ri.requests().set(j, RequestKindRuntime, "", RequestInstalling)
	}
	exit(75)
	return nil
}

// Revert implements Installer: `current` back to the runtime that ran before.
func (ri *RuntimeInstaller) Revert(ctx context.Context, j *Journal) error {
	ri.requests().remove()
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
	ri.requests().remove()
	_ = ri.Runtime.RaiseWatermark(layout.NormalizeVersion(j.Job.Version))
	_ = keepSigned(ri.Runtime.Root, j.Signed)
	keep := j.Job.KeepVersions
	if keep < 2 {
		keep = 2
	}
	// what a prune leaves is pruned by the next commit; the release is kept either way
	_, _ = ri.Runtime.Prune(ctx, keep)
	return nil
}

// Discard implements Installer.
func (ri *RuntimeInstaller) Discard(_ context.Context, j *Journal) error {
	ri.requests().remove()
	if j.Staged == "" || j.Staged == "running" {
		return nil
	}
	cur, _ := ri.Runtime.Current()
	if j.Staged == cur || j.Staged == ri.Runtime.Previous() {
		return nil
	}
	return os.RemoveAll(filepath.Join(ri.Runtime.Root, j.Staged))
}
