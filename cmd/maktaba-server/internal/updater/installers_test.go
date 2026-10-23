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
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
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
	if err := li.Stage(ctx, j, nil); err != nil {
		t.Fatal(err)
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
	feed, _ := json.Marshal(map[string]any{"version": "1.5.0", "platforms": map[string]any{key + "-app": map[string]any{}}})
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
	// the app was not replaced: a revert leaves it running its own payload
	if err := ai.Revert(ctx, j); err != nil {
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
	// a release without an app for this platform is refused before anything changes
	other, _ := json.Marshal(map[string]any{"version": "1.5.0", "platforms": map[string]any{"plan9-mips": map[string]any{}}})
	rs.files["v1.5.0/latest.json"] = other
	if err := ai.Preflight(ctx, j); err == nil {
		t.Fatal("a feed without this platform passed")
	}
}

func appOfForTest(payload string) string { return filepath.Dir(filepath.Dir(filepath.Dir(payload))) }
