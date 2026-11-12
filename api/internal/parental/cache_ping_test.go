package parental

// Review of the filter, finding 9: the policy cache checks its listener's connection
// itself. No database: the listener is a ping the test answers.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestCachePingsItsListener(t *testing.T) {
	oldEvery, oldTimeout := pingEvery, pingTimeout
	pingEvery, pingTimeout = 5*time.Millisecond, 40*time.Millisecond
	defer func() { pingEvery, pingTimeout = oldEvery, oldTimeout }()

	c := NewCache(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mode atomic.Int32 // 0: answers, 1: fails, 2: never answers
	hang := make(chan struct{})
	defer close(hang)
	var pings atomic.Int32
	ping := func() error {
		pings.Add(1)
		switch mode.Load() {
		case 1:
			return errors.New("no connection")
		case 2:
			<-hang
			return errors.New("the connection died")
		}
		return nil
	}
	listened := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.listenOn(ctx, ping, make(chan *pq.Notification), listened, make(chan struct{}), "dsn", nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	until := func(what string, want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for c.Listening() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: Listening() stayed %v", what, !want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	listened <- nil
	until("the listener connected", true)
	// kept while the pings answer
	before := pings.Load()
	for pings.Load() < before+5 {
		time.Sleep(time.Millisecond)
	}
	if !c.Listening() {
		t.Fatal("the cache stopped keeping entries while its listener answered")
	}

	// a ping that fails: nothing is kept; one that answers again: kept again
	mode.Store(1)
	until("a failing ping", false)
	mode.Store(0)
	until("the ping answers again", true)

	// a connection that died without a word: the ping never answers
	mode.Store(2)
	until("a ping with no answer", false)
	before = pings.Load()
	time.Sleep(10 * pingEvery)
	if got := pings.Load(); got > before+1 {
		t.Errorf("%d more pings were sent while one was still out", got-before)
	}
	if c.Listening() {
		t.Error("the cache keeps entries again though the ping never answered")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("listenOn did not stop")
	}
}
