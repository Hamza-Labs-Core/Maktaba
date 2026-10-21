package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Epic 36 story 36.1: a First process (the bundled postgres) starts alone, the
// others wait until its check answers, and on shutdown it stops after them.
func TestFirstStartsBeforeAndStopsAfterTheOthers(t *testing.T) {
	db, _ := fakeProc(t, RolePostgres, "serve")
	var ready atomic.Bool
	db.Check = func(context.Context) error {
		if !ready.Load() {
			return errors.New("the database system is starting up")
		}
		return nil
	}
	db.First = true
	api, _ := fakeProc(t, RoleAPI, "serve")
	s := New([]Process{api, db}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)

	waitFor(t, s, "postgres started", func(s Snapshot) bool { return proc(s, RolePostgres).PID > 0 })
	time.Sleep(300 * time.Millisecond)
	if p := proc(s.Snapshot(), RoleAPI); p.PID != 0 {
		t.Fatalf("api started (pid %d) before postgres answered", p.PID)
	}
	ready.Store(true)
	waitFor(t, s, "api running", runningPID(RoleAPI))

	if err := stop(); err != nil {
		t.Fatal(err)
	}
	var apiExit, dbExit int64
	for _, e := range s.Snapshot().Events {
		if e.Kind == EventExited && e.Role == RoleAPI {
			apiExit = e.Seq
		}
		if e.Kind == EventExited && e.Role == RolePostgres {
			dbExit = e.Seq
		}
	}
	if apiExit == 0 || dbExit == 0 || dbExit < apiExit {
		t.Fatalf("postgres must stop after the api (exit events: api #%d, postgres #%d)", apiExit, dbExit)
	}
}

// A shutdown while the First process never gets ready stops cleanly and never
// starts the others.
func TestShutdownWhileWaitingForFirst(t *testing.T) {
	db, _ := fakeProc(t, RolePostgres, "serve")
	db.Check = func(context.Context) error { return errors.New("not yet") }
	db.First = true
	api, _ := fakeProc(t, RoleAPI, "serve")
	s := New([]Process{db, api}, testPolicy(), WithLog(&syncBuffer{}))
	stop, _ := running(t, s)
	waitFor(t, s, "postgres started", func(s Snapshot) bool { return proc(s, RolePostgres).PID > 0 })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if p := proc(snap, RoleAPI); p.State != StateStopped || p.LastExit != nil {
		t.Fatalf("api = %+v, want stopped and never started", p)
	}
}
