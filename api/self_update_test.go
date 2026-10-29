package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

type fakeSup struct {
	st  *updates.MachineStatus
	err error
	n   int
}

func (f *fakeSup) Do(context.Context, string, any) (*updates.MachineStatus, error) {
	f.n++
	return f.st, f.err
}
func (f *fakeSup) Log(context.Context, string) ([]byte, error) { return nil, nil }

// A paused machine's page says what its own update is doing: in flight (with its release),
// or nothing once the run is over; a supervisor that does not answer keeps the last word.
func TestSupervisorUpdateSaysTheRunInFlight(t *testing.T) {
	ctx := context.Background()
	job := &updates.MachineJob{RunID: "r1", Version: "v0.3.43"}
	for _, tc := range []struct {
		state string
		want  bool
	}{
		{updates.MPreparing, true}, {updates.MOrdered, true}, {updates.MWatching, true},
		{updates.MCommitted, false}, {updates.MRolledBack, false}, {updates.MAborted, false},
	} {
		got := supervisorUpdate(&fakeSup{st: &updates.MachineStatus{Job: job, State: tc.state}})(ctx)
		if (got != nil) != tc.want {
			t.Fatalf("%s: %+v", tc.state, got)
		}
		if got != nil && (got.Version != "0.3.43" || got.State != tc.state) {
			t.Fatalf("%s: %+v", tc.state, got)
		}
	}
	if got := supervisorUpdate(&fakeSup{})(ctx); got != nil {
		t.Fatalf("no journal: %+v", got)
	}
	if got := supervisorUpdate(&fakeSup{err: errors.New("unreachable")})(ctx); got != nil {
		t.Fatalf("unreachable: %+v", got)
	}
	// asked at most every selfUpdateEvery
	f := &fakeSup{st: &updates.MachineStatus{Job: job, State: updates.MWatching}}
	read := supervisorUpdate(f)
	read(ctx)
	read(ctx)
	if f.n != 1 {
		t.Fatalf("asked %d times", f.n)
	}
}
