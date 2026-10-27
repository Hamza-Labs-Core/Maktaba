package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const testRepo = rv.DefaultRepo

var testPlatform = runtime.GOOS + "/" + runtime.GOARCH

// releases serves fake signed releases over HTTP, as GitHub's asset URLs would.
type releases struct {
	ss    *releaseverifytest.Sigstore
	tm    root.TrustedMaterial
	srv   *httptest.Server
	files map[string][]byte // "<tag>/<name>"
}

func newReleases(t *testing.T) *releases {
	t.Helper()
	ss := releaseverifytest.MustNew()
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	r := &releases{ss: ss, tm: tm, files: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/")]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// publish signs a release and returns its asset map (name -> URL).
func (r *releases) publish(t *testing.T, tag string, o releaseverifytest.ReleaseOptions, extra map[string][]byte) (*releaseverifytest.FakeRelease, map[string]string) {
	t.Helper()
	rel, err := r.ss.NewReleaseWith(testRepo, tag, testPlatform, o)
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string]string{}
	for n, b := range rel.Files {
		r.files[tag+"/"+n] = b
		assets[n] = r.srv.URL + "/" + tag + "/" + n
	}
	for n, b := range extra { // unsigned extras (the app's updater feed)
		r.files[tag+"/"+n] = b
		assets[n] = r.srv.URL + "/" + tag + "/" + n
	}
	return rel, assets
}

func (r *releases) source() Source {
	id, _ := rv.NewIdentity(testRepo)
	return Source{Identity: id, HTTP: r.srv.Client(),
		Trusted: func(context.Context) (root.TrustedMaterial, error) { return r.tm, nil }}
}

func jobFor(tag string, assets map[string]string) Job {
	return Job{RunID: "run-1", Version: strings.TrimPrefix(tag, "v"), Tag: tag, Assets: assets, Home: true,
		WatchSeconds: 30, KeepVersions: 2}
}

// The side-by-side layout: staged with its migrations, switched to under the launcher's
// guard (with the deadline and the database record), switched back, committed.
func TestLayoutInstaller(t *testing.T) {
	rs := newReleases(t)
	ctx := context.Background()
	l := layout.New(t.TempDir())
	old, _ := rs.publish(t, "v1.4.0", releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.0.0"}, nil)
	get := func(_ context.Context, u string, _ int64) ([]byte, error) {
		return old.Files[filepath.Base(u)], nil
	}
	assetsOld := map[string]string{}
	for n := range old.Files {
		assetsOld[n] = "mem://" + n
	}
	if _, err := install.Stage(ctx, l, get, rv.Input{Tag: "v1.4.0", Identity: rs.source().Identity, TrustedMaterial: rs.tm},
		assetsOld, testPlatform); err != nil {
		t.Fatal(err)
	}
	if err := l.WritePointer(layout.CurrentFile, "1.4.0"); err != nil {
		t.Fatal(err)
	}
	_, assets := rs.publish(t, "v1.5.0", releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.4.0",
		Migrations: map[string][]byte{"0001_a.sql": []byte("-- +goose Up\nSELECT 1;\n")}}, nil)
	record := filepath.Join(t.TempDir(), "database.json")
	li := &LayoutInstaller{Layout: l, Source: rs.source(), Version: "1.4.0", Record: record}
	j := &Journal{Job: jobFor("v1.5.0", assets), From: "1.4.0"}
	if err := li.Preflight(ctx, j); err != nil {
		t.Fatal(err)
	}
	// review A12: the release's pipeline environment is built from its verified wheel and
	// constraints, with the machine's Python, and keeps the extras of the environment it
	// replaces (review round 2: a worker's extras are not dropped)
	t.Setenv(envPipelinePython, "/opt/python3.12/bin/python3")
	curVenv := filepath.Join(l.ReleaseDir("1.4.0"), "venv")
	if err := os.MkdirAll(filepath.Dir(venvPython(curVenv)), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(venvPython(curVenv), nil, 0o755)
	_ = os.WriteFile(filepath.Join(curVenv, venvStamp), []byte(`{"extras": ["stt", "search", "mediaintel", "dev"]}`), 0o644)
	var built appvenv.Spec
	prevBuild := venvBuild
	venvBuild = func(_ context.Context, spec appvenv.Spec, _ func(appvenv.Progress)) (appvenv.Result, error) {
		built = spec
		return appvenv.Result{}, os.MkdirAll(spec.Venv, 0o755)
	}
	t.Cleanup(func() { venvBuild = prevBuild })
	if err := li.Stage(ctx, j, nil); err != nil {
		t.Fatal(err)
	}
	if built.Python != "/opt/python3.12/bin/python3" || built.Venv != filepath.Join(l.ReleaseDir("1.5.0"), "venv") ||
		built.Extras != "mediaintel,search,stt" {
		t.Fatalf("venv built %+v", built)
	}
	for _, f := range []string{"maktaba_pipeline-1.5.0-py3-none-any.whl", "maktaba-pipeline-1.5.0-constraints.txt"} {
		if _, err := os.Stat(filepath.Join(built.Pipeline, f)); err != nil {
			t.Fatalf("the pipeline folder lacks %s: %v", f, err)
		}
	}
	if j.Staged != l.ReleaseDir("1.5.0") || j.FromRef != "1.4.0" {
		t.Fatalf("staged %q from %q", j.Staged, j.FromRef)
	}
	api, mig := li.Tools(j)
	if api == "" || filepath.Base(mig) != "migrations" {
		t.Fatalf("tools %q %q", api, mig)
	}
	if err := li.Switch(ctx, j); err != nil {
		t.Fatal(err)
	}
	p, _ := l.ReadPending()
	if cur, _ := l.Current(); cur != "1.5.0" || p == nil || p.Record != record || p.Deadline.Before(time.Now().Add(20*time.Minute)) {
		t.Fatalf("after the switch: current %q pending %+v", cur, p)
	}
	if err := li.Revert(ctx, j); err != nil {
		t.Fatal(err)
	}
	if cur, _ := l.Current(); cur != "1.4.0" {
		t.Fatalf("after the revert: current %q", cur)
	}
	if p, _ := l.ReadPending(); p != nil {
		t.Fatalf("pending left: %+v", p)
	}
	_ = li.Switch(ctx, j)
	if err := li.Commit(ctx, j); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.ReadPending(); p != nil || l.Watermark() != "1.5.0" {
		t.Fatalf("after the commit: pending %+v watermark %q", p, l.Watermark())
	}
	// a release that is not signed by the pinned workflow is never staged
	j2 := &Journal{Job: jobFor("v1.5.0", assets), From: "1.4.0"}
	j2.Job.Tag, j2.Job.Version = "v1.6.0", "1.6.0"
	if err := li.Stage(ctx, j2, nil); err == nil {
		t.Fatal("a release named for another tag was staged")
	}
}

// writePayload makes a runtime payload directory of version (payload.json, the
// manifest, a server binary).
func writePayload(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"bin/maktaba-server": "#!fake server " + version + "\n",
		"bin/maktaba-api": "#!fake api " + version + "\n", "migrations/0001_a.sql": "-- +goose Up\n"}
	var lines []string
	var total int64
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		mode := os.FileMode(0o644)
		if strings.HasPrefix(p, "bin/") {
			mode = 0o755
		}
		_ = os.WriteFile(full, []byte(body), mode)
		_ = os.Chmod(full, mode)
		sum := sha256.Sum256([]byte(body))
		lines = append(lines, fmt.Sprintf("f\t%04o\t%d\t%s\t%s", mode, len(body), hex.EncodeToString(sum[:]), p))
		total += int64(len(body))
	}
	sort.Strings(lines)
	manifest := strings.Join(lines, "\n") + "\n"
	_ = os.WriteFile(filepath.Join(dir, appruntime.Manifest), []byte(manifest), 0o644)
	ms := sha256.Sum256([]byte(manifest))
	pj, _ := json.Marshal(map[string]any{"schema": 1, "version": version, "platform": appruntime.Platform(),
		"files": map[string]any{"manifest": appruntime.Manifest, "count": len(lines), "bytes": total,
			"sha256": hex.EncodeToString(ms[:])}})
	_ = os.WriteFile(filepath.Join(dir, appruntime.PayloadJSON), pj, 0o644)
	return dir
}

// The Linux runtime copy: current moves to the new runtime (no service stop: the
// supervisor restarts itself) under the launcher's guard, back on a revert, the guard
// gone and the watermark up on a commit.
func TestRuntimeInstallerPointsAndReverts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the runtime copy is the Linux app's (symbolic links)")
	}
	ctx := context.Background()
	rt := appruntime.New(filepath.Join(t.TempDir(), "runtime"))
	rt.Sync = func() {}
	first, err := rt.Install(ctx, writePayload(t, "1.4.0"), nil, appruntime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := rt.Install(ctx, writePayload(t, "1.5.0"), nil, appruntime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lroot := t.TempDir()
	ri := &RuntimeInstaller{Runtime: rt, Version: "1.4.0", LauncherRoot: lroot}
	j := &Journal{Job: jobFor("v1.5.0", nil), From: "1.4.0", Staged: filepath.Base(second.Dir), FromRef: filepath.Base(first.Dir)}
	if err := ri.Preflight(ctx, j); err != nil {
		t.Fatal(err)
	}
	if api, mig := ri.Tools(j); api == "" || mig == "" {
		t.Fatalf("tools %q %q", api, mig)
	}
	if err := ri.Switch(ctx, j); err != nil {
		t.Fatal(err)
	}
	g, _ := layout.ReadGuard(lroot)
	if cur, _ := rt.Current(); cur != filepath.Base(second.Dir) || g == nil || g.PreviousRuntime != filepath.Base(first.Dir) {
		t.Fatalf("after the switch: current %q guard %+v", cur, g)
	}
	if err := ri.Revert(ctx, j); err != nil {
		t.Fatal(err)
	}
	if cur, _ := rt.Current(); cur != filepath.Base(first.Dir) {
		t.Fatalf("after the revert: %q", cur)
	}
	_ = ri.Switch(ctx, j)
	if err := ri.Commit(ctx, j); err != nil {
		t.Fatal(err)
	}
	if g, _ := layout.ReadGuard(lroot); g != nil || rt.Watermark() != "1.5.0" {
		t.Fatalf("after the commit: guard %+v watermark %q", g, rt.Watermark())
	}
}

// The desktop app's bundle: the release verified and the app's feed checked, a copy of
// the running payload kept, the app asked (and started when it does not answer), and on
// a revert the launcher told to run the kept copy once the app holds the new release.
func TestAppInstaller(t *testing.T) {
	rs := newReleases(t)
	ctx := context.Background()
	key := strings.Replace(feedPlatform(), "/", "-", 1)
	appURL := rs.srv.URL + "/v1.5.0/Maktaba.app.tar.gz"
	feed, _ := json.Marshal(map[string]any{"version": "1.5.0", "platforms": map[string]any{key + "-app": map[string]any{"url": appURL}}})
	_, assets := rs.publish(t, "v1.5.0", releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.4.0"},
		map[string][]byte{"latest.json": feed})
	bundled := filepath.Join(t.TempDir(), "Maktaba.app", "Contents", "Resources", "server")
	_ = os.MkdirAll(filepath.Join(bundled, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(bundled, "bin", layout.Exe("maktaba-server")), []byte("#!server 1.4.0"), 0o755)
	_ = os.WriteFile(filepath.Join(bundled, "payload.json"), []byte(`{"schema":1,"version":"1.4.0"}`), 0o644)
	lroot := t.TempDir()
	var opened []string
	ai := &AppInstaller{Payload: bundled, Bundled: bundled, App: appOfForTest(bundled), LauncherRoot: lroot,
		Source: rs.source(), Version: "1.4.0", Record: "/state/database.json",
		NudgeAfter: 10 * time.Millisecond, WaitApp: 2 * time.Second, PollEvery: 5 * time.Millisecond,
		Open: func(_ context.Context, url string) error {
			opened = append(opened, url)
			// the app answers: it cannot install
			req, _ := json.Marshal(AppRequest{RunID: "run-1", Version: "1.5.0", State: "failed", Error: "no room"})
			return os.WriteFile(filepath.Join(lroot, "updates", AppRequestFile), req, 0o600)
		}}
	j := &Journal{Job: jobFor("v1.5.0", assets), From: "1.4.0"}
	j.Job.Home = false
	if err := ai.Preflight(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := ai.Stage(ctx, j, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(j.FromRef, keptMarker)); err != nil {
		t.Fatalf("no kept copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(j.FromRef, "bin", layout.Exe("maktaba-server"))); err != nil {
		t.Fatalf("the kept copy has no server: %v", err)
	}
	if err := ai.Switch(ctx, j); err != nil {
		t.Fatal(err)
	}
	if g, _ := layout.ReadGuard(lroot); g == nil || g.PreviousPayload != j.FromRef || g.Record != "/state/database.json" {
		t.Fatalf("guard %+v", g)
	}
	err := ai.Restart(ctx, j, nil)
	var f *Failure
	if !errors.As(err, &f) || f.Code != "app_install_failed" || len(opened) != 1 || !strings.HasPrefix(opened[0], AppInstallURL) {
		t.Fatalf("restart: %v opened %v", err, opened)
	}
	// the app's payload.json still says 1.4.0, but an install cut short may have replaced
	// its programs already: the kept copy runs (review B-M1)
	if err := ai.Revert(ctx, j); err != nil {
		t.Fatal(err)
	}
	if o := layout.ReadOverride(lroot); o != j.FromRef {
		t.Fatalf("override %q, want the kept copy", o)
	}
	// no copy kept, the app not replaced: it runs its own payload
	noCopy := *j
	noCopy.FromRef = ""
	if err := ai.Revert(ctx, &noCopy); err != nil {
		t.Fatal(err)
	}
	if o := layout.ReadOverride(lroot); o != "" {
		t.Fatalf("override %q", o)
	}
	// the app holds 1.5.0 now (its install went through, the new server failed): the
	// launcher runs the kept 1.4.0
	_ = os.WriteFile(filepath.Join(bundled, "payload.json"), []byte(`{"schema":1,"version":"1.5.0"}`), 0o644)
	_ = ai.Switch(ctx, j)
	if err := ai.Revert(ctx, j); err != nil {
		t.Fatal(err)
	}
	if o := layout.ReadOverride(lroot); o != j.FromRef {
		t.Fatalf("override %q, want %q", o, j.FromRef)
	}
	// a commit runs the app's own payload again and keeps only the copy to go back to
	stale := ai.keptDir("1.3.0")
	_ = os.MkdirAll(stale, 0o700)
	if err := ai.Commit(ctx, j); err != nil {
		t.Fatal(err)
	}
	if o := layout.ReadOverride(lroot); o != "" {
		t.Fatalf("override after the commit %q", o)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("an older kept copy was left")
	}
	if _, err := os.Stat(j.FromRef); err != nil {
		t.Fatal("the copy to go back to was removed")
	}
	// a release without an app for this platform is refused before anything changes (the
	// app on 1.4.0 again)
	_ = os.WriteFile(filepath.Join(bundled, "payload.json"), []byte(`{"schema":1,"version":"1.4.0"}`), 0o644)
	other, _ := json.Marshal(map[string]any{"version": "1.5.0", "platforms": map[string]any{"plan9-mips": map[string]any{}}})
	rs.files["v1.5.0/latest.json"] = other
	if err := ai.Preflight(ctx, j); err == nil {
		t.Fatal("a feed without this platform passed")
	}
	// review B-M6: an app of another release, or a feed not beside this release.json
	elsewhere, _ := json.Marshal(map[string]any{"version": "1.5.0", "platforms": map[string]any{key + "-app": map[string]any{
		"url": rs.srv.URL + "/v1.4.0/Maktaba.app.tar.gz"}}})
	rs.files["v1.5.0/latest.json"] = elsewhere
	if err := ai.Preflight(ctx, j); err == nil || !strings.Contains(err.Error(), "not a file of") {
		t.Fatalf("an app of another release passed: %v", err)
	}
	moved := *j
	moved.Job.Assets = map[string]string{"release.json": j.Job.Assets["release.json"], "latest.json": rs.srv.URL + "/v1.4.0/latest.json"}
	if err := ai.Preflight(ctx, &moved); err == nil || !strings.Contains(err.Error(), "not this release's") {
		t.Fatalf("a feed of another tag passed: %v", err)
	}
	// review B-M2: the app holds the release (a retry after its rollback): nothing to fetch,
	// the order only shows it, and at the go the server starts it itself
	_ = os.WriteFile(filepath.Join(bundled, "payload.json"), []byte(`{"schema":1,"version":"1.5.0"}`), 0o644)
	if err := ai.Preflight(ctx, &moved); err != nil {
		t.Fatalf("a held release: %v", err)
	}
	if kind, feed := ai.orderKind(j); kind != RequestKindServer || feed != "" {
		t.Fatalf("order kind %q feed %q", kind, feed)
	}
	exited := 0
	if err := ai.Restart(ctx, j, func(code int) { exited = code }); err != nil || exited != 75 {
		t.Fatalf("restart of a held release: %v exit %d", err, exited)
	}
}

func appOfForTest(payload string) string { return filepath.Dir(filepath.Dir(filepath.Dir(payload))) }

// The order the server and the desktop app share (D1, request.go): ordered with the
// release's own feed and when it installs, the app's answers read back (staged; an older
// app's clicked read as staged), the app started when it does not answer, go at the
// home's word, withdrawn by a revert.
func TestAppOrderProtocol(t *testing.T) {
	lroot := t.TempDir()
	// the installer's clock is the test's (review round 2: no real-time window to miss
	// under load); only waiting for the go polls in real time, with room to spare
	var opened atomic.Int32
	var mu sync.Mutex
	clock := time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	ai := &AppInstaller{LauncherRoot: lroot, Version: "1.4.0", NudgeAfter: time.Minute, WaitApp: time.Hour,
		PollEvery: 2 * time.Millisecond, Now: now, Open: func(context.Context, string) error { opened.Add(1); return nil }}
	feed := "https://github.com/o/r/releases/download/v1.5.0/latest.json"
	j := &Journal{Job: jobFor("v1.5.0", map[string]string{"latest.json": feed}), From: "1.4.0"}
	j.Job.Home = true
	path := filepath.Join(lroot, "updates", AppRequestFile)
	appSays := func(state, why string) {
		t.Helper()
		var q AppRequest
		b, _ := os.ReadFile(path)
		_ = json.Unmarshal(b, &q)
		q.State, q.Error = state, why
		b, _ = json.Marshal(q)
		if err := layout.WriteFileAtomic(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !ai.Orders() {
		t.Fatal("a desktop app is not ordered")
	}
	at := time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC)
	j.InstallAt = &at
	if err := ai.Order(j); err != nil {
		t.Fatal(err)
	}
	var q AppRequest
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &q); err != nil || q.State != RequestOrdered || q.Kind != RequestKindApp || !q.Home ||
		q.Feed != feed || q.RunID != j.Job.RunID || q.InstallAt == nil || !q.InstallAt.Equal(at) {
		t.Fatalf("order %+v (%v)", q, err)
	}
	if s, err := ai.Answer(j); err != nil || s != RequestOrdered || opened.Load() != 0 {
		t.Fatalf("answer %q %v opened %d", s, err, opened.Load())
	}
	advance(2 * time.Minute)
	if s, _ := ai.Answer(j); s != RequestOrdered || opened.Load() != 1 {
		t.Fatalf("no answer for a while: %q opened %d", s, opened.Load())
	}
	appSays(RequestStaged, "")
	_ = ai.Order(j) // asked again (a restart): the app's answer stays
	if s, _ := ai.Answer(j); s != RequestStaged {
		t.Fatalf("answer %q", s)
	}
	// an app from before the click was removed says clicked: staged, the go needs no more
	appSays("clicked", "")
	if s, _ := ai.Answer(j); s != RequestStaged {
		t.Fatalf("an older app's clicked: answer %q", s)
	}
	// the home's go: the app takes it (installing), then cannot install
	done := make(chan error, 1)
	go func() { done <- ai.Restart(context.Background(), j, nil) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		_ = json.Unmarshal(b, &q)
		if q.State == RequestGo || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if q.State != RequestGo {
		t.Fatalf("no go: %+v", q)
	}
	appSays(RequestInstalling, "")
	appSays(RequestFailed, "no room")
	var f *Failure
	if err := <-done; !errors.As(err, &f) || f.Code != "app_install_failed" {
		t.Fatalf("restart: %v", err)
	}
	if opened.Load() != 1 {
		t.Fatalf("the app was started again while it answered: %d", opened.Load())
	}
	// an app this user cannot replace (macOS, no admin prompt for an ordered install):
	// needs_admin, a person's
	appSays(RequestFailed, "needs_admin: /Applications is not writable by this user")
	if _, err := ai.Answer(j); !errors.As(err, &f) || f.Code != "needs_admin" || strings.HasPrefix(f.Detail, "needs_admin") {
		t.Fatalf("needs_admin: %v", err)
	}
	// an app that failed to stage: the answer is the failure
	_ = ai.Discard(context.Background(), j)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the order outlived its discard")
	}
	if _, err := ai.Answer(j); !errors.As(err, &f) || f.Code != "app_order_gone" {
		t.Fatalf("answer without an order: %v", err)
	}

	// the Linux runtime: only the app runtime is ordered, and its order names nothing to fetch
	ri := &RuntimeInstaller{LauncherRoot: lroot, Owner: "headless"}
	if ri.Orders() {
		t.Fatal("a headless runtime is ordered")
	}
	ri.Owner = "app"
	if !ri.Orders() {
		t.Fatal("the app's runtime is not ordered")
	}
	if err := ri.Order(j); err != nil {
		t.Fatal(err)
	}
	var rq AppRequest
	b, _ = os.ReadFile(path)
	if err := json.Unmarshal(b, &rq); err != nil || rq.Kind != RequestKindRuntime || rq.Feed != "" || rq.State != RequestOrdered {
		t.Fatalf("runtime order %+v", rq)
	}
}

// Review round 2 (A12): without a bootstrap's record the environment is asked which
// extras it has; a pip that cannot reach PyPI is the machine's (venv_network, a person's),
// not the release's.
func TestReleaseVenvExtrasAndNetwork(t *testing.T) {
	l := layout.New(t.TempDir())
	if err := l.WritePointer(layout.CurrentFile, "1.4.0"); err != nil {
		t.Fatal(err)
	}
	li := &LayoutInstaller{Layout: l, Version: "1.4.0"}
	if got := li.extras(context.Background()); got != "" {
		t.Fatalf("no environment: extras %q", got)
	}
	py := venvPython(filepath.Join(l.ReleaseDir("1.4.0"), "venv"))
	_ = os.MkdirAll(filepath.Dir(py), 0o755)
	_ = os.WriteFile(py, nil, 0o755)
	prev := probeExtras
	defer func() { probeExtras = prev }()
	probeExtras = func(_ context.Context, asked string) []string {
		if asked != py {
			t.Errorf("asked %s, want %s", asked, py)
		}
		return []string{"stt", "dev"}
	}
	if got := li.extras(context.Background()); got != "stt" {
		t.Fatalf("probed extras %q", got)
	}
	for msg, want := range map[string]bool{
		"pip: Could not find a version that satisfies the requirement numpy==2.1.0":                         true,
		"HTTPSConnectionPool(host='pypi.org'): Max retries exceeded (Failed to establish a new connection)": true,
		"[Errno 11001] getaddrinfo failed":                        true,
		"verify failed: the constraints do not cover torch":       false,
		"error: Microsoft Visual C++ 14.0 or greater is required": false,
	} {
		if networkFailure(msg) != want {
			t.Errorf("networkFailure(%q) = %v", msg, !want)
		}
	}
}

// Review A11: an install without room for the release fails its preflight (disk_space,
// which the home reports as needing a person), before anything is fetched.
func TestPreflightNeedsRoom(t *testing.T) {
	prev := freeDisk
	defer func() { freeDisk = prev }()
	freeDisk = func(string) uint64 { return 1 << 30 }
	l := layout.New(t.TempDir())
	li := &LayoutInstaller{Layout: l, Version: "1.4.0"}
	var f *Failure
	if err := li.Preflight(context.Background(), nil); !errors.As(err, &f) || f.Code != "disk_space" {
		t.Fatalf("1 GiB free: %v", err)
	}
	freeDisk = func(string) uint64 { return 0 } // unknown: passes this check
	if err := checkRoom(t.TempDir(), roomRelease); err != nil {
		t.Fatal(err)
	}
}

// Review round 3: pip's "no matching distribution" on a machine whose Python is not the
// one the release's pins were made for (release.json's pipeline.python) is that Python's
// (venv_python_unsupported, naming both), not PyPI out of reach; on the right Python it is
// the package source's (venv_network). The second stage is a prepare tried again: what the
// first fetched into the release's pipeline folder is kept, not refused.
func TestReleaseVenvOnAnotherPython(t *testing.T) {
	rs := newReleases(t)
	ctx := context.Background()
	l := layout.New(t.TempDir())
	_, assets := rs.publish(t, "v1.5.0", releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.4.0"}, nil)
	if err := l.WritePointer(layout.CurrentFile, "1.4.0"); err != nil {
		t.Fatal(err)
	}
	li := &LayoutInstaller{Layout: l, Source: rs.source(), Version: "1.4.0"}
	t.Setenv(envPipelinePython, "/opt/python3.13/bin/python3")
	prevBuild, prevVersion := venvBuild, pythonVersion
	t.Cleanup(func() { venvBuild, pythonVersion = prevBuild, prevVersion })
	venvBuild = func(context.Context, appvenv.Spec, func(appvenv.Progress)) (appvenv.Result, error) {
		return appvenv.Result{}, &appvenv.Error{Message: "ERROR: Could not find a version that satisfies the requirement " +
			"torch==2.5.1 (from versions: none)\nERROR: No matching distribution found for torch==2.5.1"}
	}
	for have, want := range map[string]string{"3.13.1": "venv_python_unsupported", "3.12.4": "venv_network"} {
		pythonVersion = func(context.Context, string) string { return have }
		j := &Journal{Job: jobFor("v1.5.0", assets), From: "1.4.0"}
		var f *Failure
		if err := li.Stage(ctx, j, nil); !errors.As(err, &f) || f.Code != want {
			t.Fatalf("Python %s: %v, want %s", have, err, want)
		}
		if want == "venv_python_unsupported" && (!strings.Contains(f.Detail, "Python 3.12") || !strings.Contains(f.Detail, "3.13.1")) {
			t.Fatalf("the detail names no versions: %s", f.Detail)
		}
	}
}

// Review round 3: a Mac app that refused the order because it cannot replace itself
// without an administrator (needs_admin) changed nothing: a switch back runs its own
// untouched payload, not the kept copy.
func TestAppRefusalNeedsAdminTouchedNothing(t *testing.T) {
	lroot := t.TempDir()
	ai := &AppInstaller{LauncherRoot: lroot, Version: "1.4.0"}
	j := &Journal{Job: jobFor("v1.5.0", nil), From: "1.4.0"}
	path := filepath.Join(lroot, "updates", AppRequestFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for state, touched := range map[string]bool{
		RequestFailed + "|needs_admin: /Applications is not this user's to change": false,
		RequestFailed + "|the installer exited 2":                                  true,
		RequestInstalling + "|":                                                    true,
		"clicked|":                                                                 false,
	} {
		st, why, _ := strings.Cut(state, "|")
		b, _ := json.Marshal(AppRequest{RunID: j.Job.RunID, Version: "1.5.0", State: st, Error: why})
		if err := layout.WriteFileAtomic(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ai.appTouched(j); got != touched {
			t.Errorf("%s %q: touched %v", st, why, got)
		}
	}
}
