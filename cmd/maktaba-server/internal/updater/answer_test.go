package updater

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Research P10: an order the app never stages ends the run (app_no_answer) at the time it
// installs by (its window's end), else three hours after the order, and never within
// half an hour of it.
func TestAnswerBy(t *testing.T) {
	at := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	j := &Journal{Steps: []StepRecord{{Step: StepOrder, State: "running", StartedAt: at}}}
	if got := answerBy(j); !got.Equal(at.Add(appAnswerLimit)) {
		t.Fatalf("no install-by: %v", got)
	}
	by := at.Add(time.Hour)
	j.InstallBy = &by
	if got := answerBy(j); !got.Equal(by) {
		t.Fatalf("install by %v: %v", by, got)
	}
	soon := at.Add(5 * time.Minute)
	j.InstallBy = &soon
	if got := answerBy(j); !got.Equal(at.Add(appAnswerAtLeast)) {
		t.Fatalf("install by %v: %v", soon, got)
	}
}

// Research P10: an app that never answers (not running, or its start failed) no longer
// leaves the run waiting forever: the order ends app_no_answer, nothing switched.
func TestOrderTheAppNeverStagesEnds(t *testing.T) {
	w := newWorld("1.4.0")
	r := askRig(t, w)
	r.m.AnswerCheckEvery = -1 // the rig's clock moves a minute a reading: look at every check
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	j := r.waitState(StateApplyFailed)
	if j.Error == nil || j.Error.Code != "app_no_answer" || j.Error.Step != StepOrder {
		t.Fatalf("error %+v", j.Error)
	}
	if w.next != "1.4.0" || w.called(StepSwitch) {
		t.Fatalf("next %s calls %v", w.next, w.calls)
	}
}

// Research P10: the Linux app's runtime order has nothing to stage, so an app that does
// not answer within runtimeNoAppAfter no longer holds the run: it goes on as staged, and
// the log says why (the app's start that failed, which was dropped before).
func TestRuntimeOrderGoesOnWithoutTheApp(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var logged []string
	opened := 0
	ri := &RuntimeInstaller{LauncherRoot: t.TempDir(), Owner: "app", Now: func() time.Time { return now },
		Open: func(context.Context, string) error { opened++; return errors.New("xdg-open: no display") },
		Logf: func(format string, a ...any) {
			mu.Lock()
			logged = append(logged, fmt.Sprintf(format, a...))
			mu.Unlock()
		}}
	j := &Journal{Job: Job{RunID: "run-1", Version: "1.5.0", Home: true}}
	if err := ri.Order(j); err != nil {
		t.Fatal(err)
	}
	start := now
	for _, m := range []time.Duration{time.Minute, 2 * time.Minute, 9 * time.Minute} {
		now = start.Add(m) // minutes after the order
		if st, err := ri.Answer(j); err != nil || st != RequestOrdered {
			t.Fatalf("after %s: %q %v", m, st, err)
		}
	}
	if opened == 0 {
		t.Fatal("the app was never started with its order")
	}
	now = time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC).Add(runtimeNoAppAfter + time.Second)
	st, err := ri.Answer(j)
	if err != nil || st != RequestStaged {
		t.Fatalf("after %s: %q %v", runtimeNoAppAfter, st, err)
	}
	if q, _ := ri.requests().read(); q.State != RequestStaged {
		t.Fatalf("order %+v", q)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "xdg-open: no display") || !strings.Contains(logged[0], "goes on") {
		t.Fatalf("logged %q", logged)
	}
}

// Research P10: a start of the desktop app with its order that fails is said in the run's
// log, once per error (the order is read every second), and the order waits on.
func TestAppNudgeErrorIsSaid(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	var logged []string
	ai := &AppInstaller{LauncherRoot: t.TempDir(), Now: func() time.Time { return now }, NudgeAfter: time.Minute,
		Open: func(context.Context, string) error { return errors.New("LaunchServices: -10814") },
		Logf: func(format string, a ...any) { logged = append(logged, fmt.Sprintf(format, a...)) }}
	j := &Journal{Job: Job{RunID: "run-1", Version: "1.5.0", Home: true}}
	if err := ai.requests().order(j, RequestKindApp, ""); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		now = now.Add(2 * time.Minute)
		if st, err := ai.Answer(j); err != nil || st != RequestOrdered {
			t.Fatalf("answer %d: %q %v", i, st, err)
		}
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "LaunchServices: -10814") {
		t.Fatalf("logged %q", logged)
	}
}

// A runtime order that goes on without its app moves only an order still waiting: an answer
// of the app that came meanwhile (staged, or failed, with its error), and an order withdrawn
// or replaced, stay as they are, and nothing is written afresh.
func TestRuntimeOrderKeepsALateAnswer(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	j := &Journal{Job: Job{RunID: "run-1", Version: "1.5.0"}}
	for name, c := range map[string]struct {
		meanwhile func(t *testing.T, r requests)
		staged    bool
		state     string // the order's state after ("": none)
	}{
		"waiting":   {func(*testing.T, requests) {}, true, RequestStaged},
		"withdrawn": {func(_ *testing.T, r requests) { r.remove() }, false, ""},
		"failed": {func(t *testing.T, r requests) {
			q, _ := r.read()
			q.State, q.Error = RequestFailed, "needs_admin: no"
			if err := r.write(q); err != nil {
				t.Fatal(err)
			}
		}, false, RequestFailed},
		"gone to the go": {func(t *testing.T, r requests) {
			if err := r.set(j, RequestKindRuntime, "", RequestGo); err != nil {
				t.Fatal(err)
			}
		}, false, RequestGo},
		"replaced": {func(t *testing.T, r requests) {
			if err := r.order(&Journal{Job: Job{RunID: "run-2", Version: "1.6.0"}}, RequestKindRuntime, ""); err != nil {
				t.Fatal(err)
			}
		}, false, RequestOrdered},
	} {
		t.Run(name, func(t *testing.T) {
			r := requests{dir: t.TempDir(), now: func() time.Time { return now }}
			if err := r.order(j, RequestKindRuntime, ""); err != nil {
				t.Fatal(err)
			}
			c.meanwhile(t, r)
			if staged, err := r.stage(j); err != nil || staged != c.staged {
				t.Fatalf("staged %v %v", staged, err)
			}
			q, err := r.read()
			if c.state == "" {
				if err == nil {
					t.Fatalf("a withdrawn order is back: %+v", q)
				}
				return
			}
			if q.State != c.state || (c.state == RequestFailed && q.Error != "needs_admin: no") ||
				(name == "replaced" && q.RunID != "run-2") {
				t.Fatalf("order %+v", q)
			}
		})
	}
	// and the run hears the late answer, not a staged order
	ri := &RuntimeInstaller{LauncherRoot: t.TempDir(), Owner: "app", Now: func() time.Time { return now }}
	if err := ri.Order(j); err != nil {
		t.Fatal(err)
	}
	q, _ := ri.requests().read()
	q.State, q.Error = RequestFailed, "needs_admin: no"
	if err := ri.requests().write(q); err != nil {
		t.Fatal(err)
	}
	now = now.Add(runtimeNoAppAfter + time.Minute)
	if st, err := ri.Answer(j); err == nil || st == RequestStaged {
		t.Fatalf("after a late failure: %q %v", st, err)
	}
}

// An order is not given up on over time the machine slept, nor before the app had half an
// hour awake with it: a laptop that slept nine hours past the order's time gives its app that
// after it wakes; one awake all along is given up on at the time.
func TestAppWaitCountsAwakeTime(t *testing.T) {
	at := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	by := at.Add(3 * time.Hour)
	looks := func(a *appWait, from, to time.Duration) (over time.Duration) {
		for d := from; d <= to; d += time.Minute {
			a.look(at.Add(d))
			if a.over(by) {
				return d
			}
		}
		return 0
	}
	var awake appWait
	if got := looks(&awake, 0, 5*time.Hour); got != 3*time.Hour+time.Minute {
		t.Fatalf("awake: given up on after %s", got)
	}
	var slept appWait
	if got := looks(&slept, 0, time.Hour); got != 0 {
		t.Fatalf("within its time: %s", got)
	}
	if got := looks(&slept, 9*time.Hour, 12*time.Hour); got != 9*time.Hour+30*time.Minute {
		t.Fatalf("awake again after 9 h: given up on after %s", got)
	}
	var started appWait // the server started long past the order's time
	if got := looks(&started, 20*time.Hour, 22*time.Hour); got != 20*time.Hour+30*time.Minute {
		t.Fatalf("a start: given up on after %s", got)
	}
	var back appWait // the clock set back while it waits
	_ = looks(&back, 2*time.Hour+50*time.Minute, 2*time.Hour+59*time.Minute)
	if got := looks(&back, 0, 4*time.Hour); got != 3*time.Hour+time.Minute {
		t.Fatalf("a clock set back: given up on after %s", got)
	}
}

// The release being judged stops pushing the launcher's guard once it ran as long as the
// launcher gives it (one from before 2026-11-24 counts none), a rollback under way later; and
// the rollback tells the guard it is under way.
func TestExtendGuardStopsAtItsRunningTime(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	root := t.TempDir()
	ri := &RuntimeInstaller{LauncherRoot: root, Now: func() time.Time { return now }}
	m := &Machine{Dir: t.TempDir(), Installer: ri, Now: func() time.Time { return now }}
	m.Start(context.Background())
	j := &Journal{Job: Job{RunID: "run-1", Version: "1.5.0", Home: true}, State: StateWatching}
	m.j = j
	if err := layout.WriteGuard(root, layout.Guard{Version: "1.5.0", RunID: "run-1", Since: now, Deadline: now,
		MaxRunSec: int64(guardRun(j) / time.Second)}); err != nil {
		t.Fatal(err)
	}
	pushed := func() bool {
		before, _ := layout.ReadGuard(root)
		m.extendGuard(now.Add(15 * time.Minute))
		after, _ := layout.ReadGuard(root)
		return after.Deadline.After(before.Deadline)
	}
	if now = now.Add(guardRun(j) - time.Minute); !pushed() {
		t.Fatal("not pushed within its time")
	}
	if now = now.Add(2 * time.Minute); pushed() {
		t.Fatal("pushed past its time")
	}
	j.State = StateRollingBack
	if now = now.Add(time.Hour); !pushed() {
		t.Fatal("a rollback under way is not given its own time")
	}
	stop := m.keepGuard()
	stop()
	if g, _ := layout.ReadGuard(root); !g.RollingBack {
		t.Fatalf("the guard does not say the rollback is under way: %+v", g)
	}
	if now = now.Add(layout.RollbackRun); pushed() {
		t.Fatal("a rollback pushed past its time")
	}
}
