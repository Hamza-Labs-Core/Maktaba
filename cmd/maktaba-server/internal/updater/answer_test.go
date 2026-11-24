package updater

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
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
