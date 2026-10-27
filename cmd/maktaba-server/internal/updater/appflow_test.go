package updater

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// fakeApp is the desktop app as the order file (app-install.json) sees it: it stages
// what it is ordered, a person clicks, and at the go it installs (its payload becomes the
// release) and its install stops the server; or it refuses with one of its codes.
type fakeApp struct {
	dir, payload string
	refuse       string // "<code>: <why>" answered instead of staging
	mu           sync.Mutex
	installed    chan struct{}
	stop         chan struct{}
}

func (a *fakeApp) order() (AppRequest, bool) {
	var q AppRequest
	b, err := os.ReadFile(filepath.Join(a.dir, AppRequestFile))
	if err != nil || json.Unmarshal(b, &q) != nil {
		return q, false
	}
	return q, true
}

func (a *fakeApp) answer(q AppRequest, state, why string) {
	q.State, q.Error, q.At = state, why, time.Now().UTC()
	b, _ := json.Marshal(q)
	_ = os.WriteFile(filepath.Join(a.dir, AppRequestFile), b, 0o600)
}

func (a *fakeApp) run() {
	for {
		select {
		case <-a.stop:
			return
		case <-time.After(2 * time.Millisecond):
		}
		q, ok := a.order()
		if !ok {
			continue
		}
		a.mu.Lock()
		switch q.State {
		case RequestOrdered:
			if a.refuse != "" {
				a.answer(q, RequestFailed, a.refuse)
			} else if q.Kind == RequestKindApp && strings.HasSuffix(q.Feed, "/latest.json") {
				a.answer(q, RequestStaged, "")
			}
		case RequestGo:
			a.answer(q, RequestInstalling, "")
			_ = os.WriteFile(filepath.Join(a.payload, "payload.json"),
				[]byte(`{"schema":1,"version":"`+q.Version+`"}`), 0o644)
			close(a.installed)
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
	}
}

// appFlow is one machine running in a desktop app's payload, its server process
// (a Machine over the real AppInstaller) startable again on another release.
type appFlow struct {
	t       *testing.T
	dir     string
	lroot   string
	payload string
	rs      *releases
	m       *Machine
	cancel  context.CancelFunc
}

func (f *appFlow) start(version string) {
	ai := &AppInstaller{Payload: f.payload, Bundled: f.payload, App: appOfForTest(f.payload), LauncherRoot: f.lroot,
		Source: f.rs.source(), Version: version, NudgeAfter: time.Hour, WaitApp: 10 * time.Second,
		PollEvery: 2 * time.Millisecond}
	c := &clock{t: time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)}
	w := newWorld(version)
	f.m = &Machine{Dir: f.dir, Installer: ai, Health: &fakeHealth{v: healthy}, Records: fakeRecords{w},
		Rehearser: &fakeRehearser{}, Now: c.now, CheckEvery: time.Millisecond, Exit: func(int) {}}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.m.Start(ctx)
	f.t.Cleanup(func() {
		cancel()
		done := make(chan struct{})
		go func() { f.m.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
}

func (f *appFlow) waitState(want string) *Journal {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j := f.m.Status().Journal; j != nil && j.State == want {
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	j := f.m.Status().Journal
	f.t.Fatalf("state %+v, want %s", j, want)
	return nil
}

func newAppFlow(t *testing.T) (*appFlow, Job) {
	t.Helper()
	rs := newReleases(t)
	key := strings.Replace(feedPlatform(), "/", "-", 1)
	feed, _ := json.Marshal(map[string]any{"version": "1.5.0", "platforms": map[string]any{
		key + "-app": map[string]any{"url": rs.srv.URL + "/v1.5.0/Maktaba.app.tar.gz"}}})
	_, assets := rs.publish(t, "v1.5.0", releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.4.0"},
		map[string][]byte{"latest.json": feed})
	payload := filepath.Join(t.TempDir(), "Maktaba.app", "Contents", "Resources", "server")
	_ = os.MkdirAll(filepath.Join(payload, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(payload, "bin", layout.Exe("maktaba-server")), []byte("#!server 1.4.0"), 0o755)
	_ = os.WriteFile(filepath.Join(payload, "payload.json"), []byte(`{"schema":1,"version":"1.4.0"}`), 0o644)
	f := &appFlow{t: t, dir: t.TempDir(), lroot: t.TempDir(), payload: payload, rs: rs}
	job := jobFor("v1.5.0", assets)
	job.Home, job.Rehearse = false, false
	return f, job
}

// The app-bundle path end to end on one machine (install-kinds.md, D1), with every part
// real but the app and the process restart: the signed release and its app feed read
// over HTTP, the running payload kept, the order to the app, its staging (no click on
// the machine: owner decision D1, 2026-10-27), the go, the app's install (its payload
// becomes 1.5.0) stopping the server, the server started again on 1.5.0 under the
// launcher's guard, watched and committed: the order gone, the guard cleared, the copy
// to go back to kept.
func TestAppBundleFlowEndToEnd(t *testing.T) {
	f, job := newAppFlow(t)
	f.start("1.4.0")
	if err := f.m.Prepare(job); err != nil {
		t.Fatal(err)
	}
	j := f.waitState(StatePrepared)
	if _, err := os.Stat(filepath.Join(j.FromRef, keptMarker)); err != nil {
		t.Fatalf("the running payload was not kept: %v", err)
	}
	app := &fakeApp{dir: filepath.Join(f.lroot, "updates"), payload: f.payload, installed: make(chan struct{}),
		stop: make(chan struct{})}
	go app.run()
	t.Cleanup(func() {
		select {
		case <-app.stop:
		default:
			close(app.stop)
		}
	})
	if err := f.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	// the app stages it by itself: nobody clicks on this machine
	f.waitState(StateAppStaged)
	q, _ := app.order()
	if q.State != RequestStaged || q.Kind != RequestKindApp || q.Version != "1.5.0" {
		t.Fatalf("the order: %+v", q)
	}
	if g, _ := layout.ReadGuard(f.lroot); g != nil {
		t.Fatalf("guarded before the go: %+v", g)
	}
	if err := f.m.Apply("run-1"); err != nil { // the home's go
		t.Fatal(err)
	}
	select {
	case <-app.installed:
	case <-time.After(10 * time.Second):
		t.Fatal("the app was not told to go")
	}
	if g, _ := layout.ReadGuard(f.lroot); g == nil || g.Version != "1.5.0" || g.PreviousPayload != j.FromRef {
		t.Fatalf("the launcher's guard: %+v", g)
	}
	// the app's install stops this server; the launcher starts the app's payload, 1.5.0
	f.cancel()
	f.m.Wait()
	f.start("1.5.0")
	f.m.Resume()
	f.waitState(StateCommitted)
	if _, ok := app.order(); ok {
		t.Fatal("the order stayed after the commit")
	}
	if g, _ := layout.ReadGuard(f.lroot); g != nil {
		t.Fatalf("the guard stayed: %+v", g)
	}
	if o := layout.ReadOverride(f.lroot); o != "" {
		t.Fatalf("an override stayed: %q", o)
	}
	if _, err := os.Stat(j.FromRef); err != nil {
		t.Fatalf("the copy to go back to went: %v", err)
	}
}

// An app built locally (the placeholder updater key) answers the order with local_build:
// the run fails with that code, a person's (the release is installed by hand once), and
// nothing changed: no guard, the app's own payload runs.
func TestAppBundleFlowLocalBuild(t *testing.T) {
	f, job := newAppFlow(t)
	f.start("1.4.0")
	if err := f.m.Prepare(job); err != nil {
		t.Fatal(err)
	}
	f.waitState(StatePrepared)
	app := &fakeApp{dir: filepath.Join(f.lroot, "updates"), payload: f.payload, installed: make(chan struct{}),
		stop: make(chan struct{}), refuse: "local_build: this app was built locally and can't update itself"}
	go app.run()
	t.Cleanup(func() { close(app.stop) })
	if err := f.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var j *Journal
	for time.Now().Before(deadline) {
		if j = f.m.Status().Journal; j != nil && j.Error != nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if j == nil || j.Error == nil || j.Error.Code != "local_build" {
		t.Fatalf("the run: %+v", j)
	}
	if g, _ := layout.ReadGuard(f.lroot); g != nil {
		t.Fatalf("guarded after a refusal: %+v", g)
	}
	if o := layout.ReadOverride(f.lroot); o != "" {
		t.Fatalf("an override after a refusal: %q", o)
	}
}
