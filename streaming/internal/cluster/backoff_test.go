package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type countingBeats struct {
	calls atomic.Int32
	err   error
}

func (c *countingBeats) Beat(context.Context, string, BeatInfo) (BeatAnswer, error) {
	c.calls.Add(1)
	return BeatAnswer{}, c.err
}

func TestLoginRefused(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&pgconn.PgError{Code: "28P01"}, true},
		{fmt.Errorf("connect: %w", &pgconn.PgError{Code: "28000"}), true},
		{&pgconn.PgError{Code: "57P01"}, false},
		{errors.New("dial tcp: connection refused"), false},
		{nil, false},
	} {
		if got := LoginRefused(c.err); got != c.want {
			t.Fatalf("%v: %v, want %v", c.err, got, c.want)
		}
	}
	if LoginBackoff(5*time.Second, 1) != 10*time.Second || LoginBackoff(5*time.Second, 20) != MaxLoginBackoff {
		t.Fatal("LoginBackoff")
	}
}

// A host whose login the home's database refuses (its access re-made since it started)
// beats later and later, not every interval for ever; any other failure keeps the beat.
func TestHostLoopBacksOffARefusedLogin(t *testing.T) {
	run := func(err error) int32 {
		store := &countingBeats{err: err}
		l := &HostLoop{Store: store, HostID: "h", Interval: 10 * time.Millisecond,
			Info: func(context.Context) BeatInfo { return BeatInfo{} }}
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		l.Run(ctx)
		return store.calls.Load()
	}
	refused := run(&pgconn.PgError{Code: "28P01", Message: "password authentication failed"})
	down := run(errors.New("connection refused"))
	// refused: 10, 20, 40, 80, 160 ms waits: about 5 beats in 400 ms; down: about 40
	if refused > 7 || down < 15 {
		t.Fatalf("beats in 400 ms: %d with the login refused (want few), %d with the home down (want one every interval)",
			refused, down)
	}
}
