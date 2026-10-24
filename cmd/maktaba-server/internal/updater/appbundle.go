package updater

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// AppInstaller is a desktop app that carries the server (Maktaba.app on a Mac, the
// Windows app; Epics 36, 38): the app's own updater replaces the whole app. The server
// orders the exact release it verified (app-install.json, which the app's status poller
// reads; the app is started with maktaba://install-update when it does not answer); the
// app stages it and shows it, and installs it once someone clicks there and the home
// says go (owner decision D1: ask first; request.go). Tauri keeps no previous app, so
// before the install the server keeps a copy of the payload it runs
// (<launcher root>/updates/payloads/<v>); a rollback makes the launcher run that copy
// (payload-override), and the launcher's guard does the same when the new server cannot
// stay up.
type AppInstaller struct {
	// Payload is the payload this server runs (MAKTABA_APP_BUNDLE): the app's own, or a
	// kept copy after a rollback.
	Payload string
	// Bundled is the app's own payload (MAKTABA_APP_BUNDLED; Payload when unset).
	Bundled string
	// App is the app: …/Maktaba.app, or <install>\Maktaba.exe.
	App string
	// LauncherRoot is the launcher's state root (MAKTABA_LAUNCHER_ROOT).
	LauncherRoot string
	Source       Source
	Version      string
	// Record is the home's database record ("" on a node).
	Record string
	// Rehearse: download the platform archive's maktaba-api and the migrations for the
	// rehearsal (the home).
	Rehearse bool
	// Open opens a URL with the system (the app's deep link); tests replace it.
	Open func(ctx context.Context, url string) error
	// NudgeAfter and WaitApp: how long the app may take to answer before it is
	// started, and to install once told to go before the run fails (90 s, 30 min).
	NudgeAfter time.Duration
	WaitApp    time.Duration
	// PollEvery is how often the app's answer is read (1 s).
	PollEvery time.Duration
	Now       func() time.Time

	nudge nudger
}

func (ai *AppInstaller) now() time.Time {
	if ai.Now != nil {
		return ai.Now()
	}
	return time.Now()
}

func (ai *AppInstaller) bundled() string {
	if ai.Bundled != "" {
		return ai.Bundled
	}
	return ai.Payload
}

func (ai *AppInstaller) updatesDir() string { return filepath.Join(ai.LauncherRoot, "updates") }

// Extend implements Extender: the launcher's guard deadline.
func (ai *AppInstaller) Extend(j *Journal, until time.Time) error {
	_, err := layout.ExtendGuard(ai.LauncherRoot, j.Job.RunID, until)
	return err
}

func (ai *AppInstaller) requests() requests { return requests{dir: ai.updatesDir(), now: ai.now} }

func (ai *AppInstaller) nudgeAfter() time.Duration {
	if ai.NudgeAfter > 0 {
		return ai.NudgeAfter
	}
	return 90 * time.Second
}

// Asks implements Asker: a desktop app always asks.
func (ai *AppInstaller) Asks() bool { return true }

// holds reports whether the app's own payload is the job's release already (a retry
// after a rollback of it: the launcher runs the kept copy of the release before).
func (ai *AppInstaller) holds(j *Journal) bool {
	p, err := apppayload.Read(filepath.Join(ai.bundled(), "payload.json"))
	return err == nil && layout.CompareVersions(p.Version, j.Job.Version) == 0
}

// orderKind is what the app does with the order: install its update, or only show it
// (it holds the release already).
func (ai *AppInstaller) orderKind(j *Journal) (kind, feed string) {
	if ai.holds(j) {
		return RequestKindServer, ""
	}
	return RequestKindApp, j.Job.Assets["latest.json"]
}

// Ask implements Asker: the order, the release's own feed named.
func (ai *AppInstaller) Ask(j *Journal) error {
	kind, feed := ai.orderKind(j)
	return ai.requests().order(j, kind, feed)
}

// Answer implements Asker; an app that has not answered within NudgeAfter is started
// with the order's deep link (once per NudgeAfter: a running app takes it too).
func (ai *AppInstaller) Answer(j *Journal) (string, error) {
	state, err := ai.requests().answer(j)
	if err == nil && state == RequestOrdered {
		ai.nudge.nudge(ai.requests(), j, ai.now(), ai.nudgeAfter(), ai.Open)
	}
	return state, err
}

// InstallFailure implements InstallReporter: the failure the app reported.
func (ai *AppInstaller) InstallFailure(j *Journal) *Failure { return ai.requests().failure(j) }

// keptDir is where the payload of version is kept.
func (ai *AppInstaller) keptDir(version string) string {
	return filepath.Join(ai.updatesDir(), "payloads", safeName(layout.NormalizeVersion(version)))
}

// Kind implements Installer.
func (ai *AppInstaller) Kind() string { return "appbundle" }

// Running implements Installer.
func (ai *AppInstaller) Running() string { return layout.NormalizeVersion(ai.Version) }

// feedPlatform is the Tauri updater's platform key of this machine (latest.json).
func feedPlatform() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return runtime.GOOS + "-" + arch
}

// Preflight implements Installer: the app is where it was, the launcher is new enough to
// keep its guard, and the release's updater feed has this platform's app.
func (ai *AppInstaller) Preflight(ctx context.Context, j *Journal) error {
	if ai.LauncherRoot == "" {
		return Fail("old_launcher", "the app's launcher is from before automatic updates: this app updates once by hand")
	}
	if _, err := os.Stat(ai.App); err != nil {
		return Fail("no_app", "the app is not at %s: %v", ai.App, err)
	}
	if err := checkRoom(ai.updatesDir(), roomRelease); err != nil {
		return err
	}
	if ai.holds(j) {
		return nil // the app holds the release: nothing to fetch for it
	}
	feed, ok := j.Job.Assets["latest.json"]
	if !ok {
		return Fail("no_app_update", "%s publishes no app update (latest.json)", j.Job.Tag)
	}
	// the feed and every app it names are files of this very release, beside its verified
	// release.json (the pinned repository's assets, of this tag); the app checks the
	// update's signature and its signed version too
	base := strings.TrimSuffix(j.Job.Assets["release.json"], "release.json")
	if base == "" || base == j.Job.Assets["release.json"] || !strings.HasSuffix(base, "/"+j.Job.Tag+"/") ||
		feed != base+"latest.json" {
		return Fail("no_app_update", "the app feed %s is not this release's (%slatest.json)", feed, base)
	}
	b, err := ai.Source.get()(ctx, feed, 1<<20)
	if err != nil {
		// a download that failed (the feed is there: its asset is listed) is tried again,
		// never a person's to mend
		return Fail("download_failed", "latest.json: %v", err)
	}
	var f struct {
		Version   string `json:"version"`
		Platforms map[string]struct {
			URL string `json:"url"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return Fail("no_app_update", "latest.json: %v", err)
	}
	if layout.CompareVersions(f.Version, j.Job.Version) != 0 {
		return Fail("no_app_update", "latest.json is %s, not %s", f.Version, j.Job.Version)
	}
	for k, p := range f.Platforms {
		if k == feedPlatform() || strings.HasPrefix(k, feedPlatform()+"-") {
			if !strings.HasPrefix(p.URL, base) {
				return Fail("no_app_update", "latest.json names %s for %s, not a file of %s", p.URL, k, base)
			}
			return nil
		}
	}
	return Fail("no_app_update", "%s has no app for %s", j.Job.Tag, feedPlatform())
}

// Stage implements Installer: the release verified (on the home also the platform
// archive's maktaba-api and the migrations, for the rehearsal), and a copy of the
// running payload kept to go back to.
func (ai *AppInstaller) Stage(ctx context.Context, j *Journal, log io.Writer) error {
	staging := filepath.Join(ai.updatesDir(), "staging", safeName(j.Job.Version))
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	var res *releaseverify.Result
	if ai.Rehearse {
		in, err := ai.Source.input(ctx, j.Job.Tag)
		if err != nil {
			return err
		}
		var up *releaseverify.ServerUpdate
		if err := retryFetch(ctx, func() error {
			var err error
			if up, err = releaseverify.FetchServer(ctx, ai.Source.get(), in, j.Job.Assets, ai.Source.platform()); err != nil {
				return verifyFailure(err)
			}
			return nil
		}); err != nil {
			return err
		}
		res = up.Result
		entry, api, err := releaseverify.ExtractBinary(up.Asset.Archive, up.Archive, "maktaba-api")
		if err != nil {
			return Fail("release_incomplete", "%v", err)
		}
		if err := res.Manifest.VerifyExtracted(ai.Source.platform(), entry, api); err != nil {
			return verifyFailure(err)
		}
		if err := os.WriteFile(filepath.Join(staging, layout.Exe("maktaba-api")), api, 0o755); err != nil {
			return err
		}
		mig, err := releaseverify.FetchMigrations(func(u string, limit int64) ([]byte, error) {
			return ai.Source.get()(ctx, u, limit)
		}, res, j.Job.Assets)
		if err != nil {
			return verifyFailure(err)
		}
		if mig != nil {
			if _, err := releaseverify.ExtractMigrations(mig, filepath.Join(staging, "migrations")); err != nil {
				return Fail("release_incomplete", "%v", err)
			}
		}
	} else if err := retryFetch(ctx, func() error {
		var err error
		res, err = ai.Source.verify(ctx, j)
		return err
	}); err != nil {
		return err
	}
	// the one "never go back" rule against the release this app committed last
	if err := checkNotBack(ai.updatesDir(), ai.Running(), res); err != nil {
		return err
	}
	j.Signed = signedOf(res)
	kept, err := ai.keep(ai.Running(), log)
	if err != nil {
		return Fail("keep_failed", "keep a copy of the running payload: %v", err)
	}
	j.Staged, j.FromRef = staging, kept
	return nil
}

// keptMarker is written last into a kept payload: a copy cut short is never run.
const keptMarker = ".kept"

// keep copies the running payload to keptDir(version) (once; on a Mac an APFS clone).
func (ai *AppInstaller) keep(version string, log io.Writer) (string, error) {
	if version == "" {
		return "", errors.New("the running version is not known")
	}
	dst := ai.keptDir(version)
	if b, err := os.ReadFile(filepath.Join(dst, keptMarker)); err == nil && strings.TrimSpace(string(b)) == version {
		return dst, nil
	}
	if sameDir(ai.Payload, dst) {
		return dst, nil // running a kept copy already (after a rollback)
	}
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	if log != nil {
		_, _ = io.WriteString(log, "[update] keeping a copy of the running payload in "+dst+"\n")
	}
	if err := copyTree(ai.Payload, dst); err != nil {
		_ = os.RemoveAll(dst)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dst, keptMarker), []byte(version+"\n"), 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func sameDir(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// copyTree copies a payload directory: files with their modes, links as links.
func copyTree(src, dst string) error {
	if cloneTree(src, dst) == nil { // an APFS clone: instant and free
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm())
		}
		return nil
	})
}

// Smoke implements Installer: the release's maktaba-api for this platform, when it was
// fetched (the app's own files are checked by its updater's signature).
func (ai *AppInstaller) Smoke(ctx context.Context, j *Journal) error {
	api := filepath.Join(j.Staged, layout.Exe("maktaba-api"))
	if _, err := os.Stat(api); err != nil {
		return nil
	}
	return smokeRun(ctx, api, j.Job.Version)
}

// Tools implements Installer.
func (ai *AppInstaller) Tools(j *Journal) (string, string) {
	api, mig := filepath.Join(j.Staged, layout.Exe("maktaba-api")), filepath.Join(j.Staged, "migrations")
	if _, err := os.Stat(api); err != nil {
		return "", ""
	}
	if st, err := os.Stat(mig); err != nil || !st.IsDir() {
		return "", ""
	}
	return api, mig
}

// Switch implements Installer: the launcher's guard naming the kept copy to fall back
// to. The override (a kept copy running after an earlier rollback) stays until the app
// holds the new release: the launcher runs the app's own payload once it is the guarded
// release, and the kept copy while the app's install has not happened (Commit clears it).
func (ai *AppInstaller) Switch(_ context.Context, j *Journal) error {
	if err := layout.WriteGuard(ai.LauncherRoot, layout.Guard{Version: layout.NormalizeVersion(j.Job.Version),
		RunID: j.Job.RunID, Since: ai.now().UTC(), Deadline: guardDeadline(ai.now(), j).UTC(),
		PreviousPayload: j.FromRef, Record: ai.Record}); err != nil {
		return Fail("switch_failed", "%v", err)
	}
	return nil
}

// AppInstallURL is the app's deep link that starts it to install what app-install.json
// asks (a running app takes it too).
const AppInstallURL = "maktaba://install-update"

// Restart implements Installer: tells the app to go (it installs what it staged) and
// waits. The app's install restarts the server (this process ends); a failure it
// reports, or no install within WaitApp, is an error (nothing was installed).
func (ai *AppInstaller) Restart(ctx context.Context, j *Journal, exit func(int)) error {
	kind, feed := ai.orderKind(j)
	if kind == RequestKindServer {
		// the app holds the release: the launcher runs its own payload under the guard
		_ = ai.requests().set(j, kind, feed, RequestInstalling)
		if exit != nil {
			exit(75)
		}
		return nil
	}
	if err := ai.requests().set(j, kind, feed, RequestGo); err != nil {
		return Fail("app_request_failed", "%v", err)
	}
	nudge, wait := ai.nudgeAfter(), ai.WaitApp
	if wait <= 0 {
		wait = 30 * time.Minute
	}
	start, nudged, installing := ai.now(), false, false
	poll := ai.PollEvery
	if poll <= 0 {
		poll = time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err() // stopped for the install: the next start goes on
		case <-t.C:
		}
		r, err := ai.requests().read()
		if err == nil && r.RunID == j.Job.RunID {
			switch r.State {
			case RequestFailed:
				code, detail := appFailure(r.Error)
				return Fail(code, "%s", detail)
			case RequestInstalling:
				if !installing {
					// it took the go: the install has WaitApp from now (once: an app that
					// dies installing must not hold the run for ever)
					start, nudged, installing = ai.now(), true, true
				}
			}
		}
		if !nudged && ai.now().Sub(start) >= nudge && ai.Open != nil {
			nudged = true
			if err := ai.Open(ctx, AppInstallURL+"?run="+j.Job.RunID); err != nil {
				return Fail("app_not_started", "the app could not be started: %v", err)
			}
		}
		if ai.now().Sub(start) >= wait {
			return Fail("app_did_not_install", "the app did not install %s within %s", j.Job.Version, wait)
		}
	}
}

// Revert implements Installer: the order withdrawn; when the app holds the new
// release already, the launcher runs the kept copy of the one before.
func (ai *AppInstaller) Revert(_ context.Context, j *Journal) error {
	touched := ai.appTouched(j)
	ai.requests().remove()
	from := layout.NormalizeVersion(j.From)
	bundled := ""
	if p, err := apppayload.Read(filepath.Join(ai.bundled(), "payload.json")); err == nil {
		bundled = layout.NormalizeVersion(p.Version)
	}
	// once the app may have changed its bundle (it began installing, or it holds another
	// release), a whole kept copy is the one that runs, whatever the app's payload.json
	// says: an install cut short may have replaced the programs and not payload.json yet
	// (review B-M1); on Windows the launcher puts the copy back into the app's folder. An
	// app that never began installing keeps running its own untouched payload.
	kept := false
	if j.FromRef != "" && (touched || bundled != from) {
		_, err := os.Stat(filepath.Join(j.FromRef, keptMarker))
		kept = err == nil || sameDir(j.FromRef, ai.Payload)
	}
	switch {
	case kept:
		if err := layout.WriteOverride(ai.LauncherRoot, j.FromRef); err != nil {
			return err
		}
	case bundled != "" && bundled == from:
		// no copy, and the app was not replaced: it runs as before
		if err := layout.WriteOverride(ai.LauncherRoot, ""); err != nil {
			return err
		}
	case j.FromRef != "":
		return Fail("no_previous", "the kept copy of %s is gone", from)
	default:
		return Fail("no_previous", "no copy of %s was kept", from)
	}
	return layout.ClearGuard(ai.LauncherRoot)
}

// appTouched says the app may have changed its bundle for j's run: its order says it
// began installing (or failed doing it). An app that refused before it began (it cannot
// replace itself without an administrator or macOS's App Management, or it was built
// locally: refusedCodes) changed nothing.
func (ai *AppInstaller) appTouched(j *Journal) bool {
	q, err := ai.requests().read()
	if err != nil || q.RunID != j.Job.RunID {
		return false
	}
	if code, _ := appFailure(q.Error); q.State == RequestFailed && refusedCodes[code] {
		return false
	}
	return q.State == RequestInstalling || q.State == RequestFailed
}

// Commit implements Installer: the guard and the request gone, the app's own payload
// runs (no override), the kept copy of the release before stays (the one to go back
// to), older kept copies go.
func (ai *AppInstaller) Commit(_ context.Context, j *Journal) error {
	ai.requests().remove()
	if err := layout.ClearGuard(ai.LauncherRoot); err != nil {
		return err
	}
	if err := layout.WriteOverride(ai.LauncherRoot, ""); err != nil {
		return err
	}
	_ = keepSigned(ai.updatesDir(), j.Signed)
	_ = os.RemoveAll(j.Staged)
	ents, _ := os.ReadDir(filepath.Join(ai.updatesDir(), "payloads"))
	for _, e := range ents {
		p := filepath.Join(ai.updatesDir(), "payloads", e.Name())
		if !sameDir(p, j.FromRef) && !sameDir(p, ai.Payload) {
			_ = os.RemoveAll(p)
		}
	}
	return nil
}

// Discard implements Installer.
func (ai *AppInstaller) Discard(_ context.Context, j *Journal) error {
	ai.requests().remove()
	if j.Staged == "" || j.Staged == "running" {
		return nil
	}
	return os.RemoveAll(j.Staged)
}

// OpenURL opens a URL with the system: LaunchServices on a Mac, the shell on Windows.
// The app it starts must outlive the server it is about to restart: on Windows the
// opener breaks away from the launcher's kill-on-close job (detach), and nothing of
// the server's environment (MAKTABA_*) reaches the app.
func OpenURL(ctx context.Context, url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", "-g", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}
	cmd.Env = withoutServerEnv(os.Environ())
	detach(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// withoutServerEnv is environ without the server's own variables (MAKTABA_*): an app
// started from here is the user's, not one of the server's children.
func withoutServerEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(strings.ToUpper(kv), "MAKTABA_") {
			out = append(out, kv)
		}
	}
	return out
}
