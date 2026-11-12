package parental_test

// Review of the filter, finding 9: the policy cache checks its listener's connection
// itself, and only the listener's own word resumes it. The listener here is a fake the
// test drives; the reads are a real migrated Postgres's.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

type pingEnv struct {
	t     *testing.T
	e     *env
	c     *parental.Cache
	pings atomic.Int32
	mode  atomic.Int32 // 0: answers, 1: fails, 2: waits for release
	gate  chan error
}

func newPingEnv(t *testing.T) *pingEnv {
	e := setup(t)
	e.restrict(uChild)
	return &pingEnv{t: t, e: e, c: parental.NewCache(e.store), gate: make(chan error)}
}

func (p *pingEnv) ping() error {
	p.pings.Add(1)
	switch p.mode.Load() {
	case 1:
		return errors.New("no connection")
	case 2:
		return <-p.gate
	}
	return nil
}

// serve starts a fake listener that is stopped, and waited for, when the test ends.
func (p *pingEnv) serve(ctx context.Context) *parental.FakeListener {
	ctx, cancel := context.WithCancel(ctx)
	l := p.c.ServeFake(ctx, p.ping)
	p.t.Cleanup(func() {
		cancel()
		<-l.Done
	})
	return l
}

func (p *pingEnv) until(what string, f func() bool) {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			p.t.Fatalf("never: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// kept reports whether the cache answers the child's policy from a kept entry: the row is
// changed with no notification the cache could hear, and the next read is compared.
func (p *pingEnv) kept() bool {
	p.t.Helper()
	ctx := context.Background()
	p.e.exec(`UPDATE parental_policies SET max_tier = 9 WHERE user_id = $1`, uChild)
	if eff, err := p.c.Effective(ctx, uChild); err != nil || eff.MaxTier != 9 {
		p.t.Fatalf("read: %+v, %v", eff, err)
	}
	p.e.exec(`UPDATE parental_policies SET max_tier = 16 WHERE user_id = $1`, uChild)
	eff, err := p.c.Effective(ctx, uChild)
	if err != nil {
		p.t.Fatal(err)
	}
	return eff.MaxTier == 9
}

func TestCachePingsItsListener(t *testing.T) {
	t.Cleanup(parental.SetPing(5*time.Millisecond, 60*time.Millisecond))
	for _, c := range []struct {
		name string
		mode int32
	}{{"a ping that fails", 1}, {"a ping with no answer", 2}} {
		t.Run(c.name, func(t *testing.T) {
			p := newPingEnv(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			l := p.serve(ctx)
			// connected alone is not listening, and listening alone is not connected
			l.Event(pq.ListenerEventConnected)
			if p.c.Listening() {
				t.Fatal("the cache keeps entries before the LISTEN was issued")
			}
			l.Listened()
			p.until("the cache keeps entries once connected and listening", p.c.Listening)
			before := p.pings.Load()
			p.until("pings are sent while it keeps entries", func() bool { return p.pings.Load() >= before+3 })
			if !p.c.Listening() || !p.kept() {
				t.Fatal("the cache stopped keeping entries while its listener answered")
			}
			p.mode.Store(c.mode)
			select {
			case <-l.Done:
			case <-time.After(5 * time.Second):
				t.Fatal("the listener was not let go")
			}
			if !l.Again {
				t.Error("no new listener was asked for")
			}
			if p.c.Listening() || p.kept() {
				t.Error("the cache keeps entries though its listener did not answer")
			}
			// whatever the old listener says now is not believed: its ping answering
			// at last, its reconnect
			if c.mode == 2 {
				p.gate <- nil
			}
			l.Event(pq.ListenerEventReconnected)
			l.Listened()
			time.Sleep(20 * time.Millisecond)
			if p.c.Listening() || p.kept() {
				t.Error("a listener that was let go resumed the cache")
			}
			// the new listener resumes it, by its own word
			p.mode.Store(0)
			n := p.serve(ctx)
			n.Listened()
			n.Event(pq.ListenerEventConnected)
			p.until("the new listener resumes the cache", p.c.Listening)
			if !p.kept() {
				t.Error("the resumed cache keeps no entries")
			}
		})
	}
}

// A ping's answer is from before whatever happened since it was sent. The connection is
// lost after a ping went out and before it answers "fine": the cache stays suspended and
// reads the database, and only the listener's reconnect resumes it.
func TestCacheIsNotResumedByAPing(t *testing.T) {
	t.Cleanup(parental.SetPing(5*time.Millisecond, 10*time.Second))
	p := newPingEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := p.serve(ctx)
	l.Event(pq.ListenerEventConnected)
	l.Listened()
	p.until("the cache keeps entries", p.c.Listening)
	if !p.kept() {
		t.Fatal("no entry is kept: the test could not tell")
	}
	p.mode.Store(2)
	sent := p.pings.Load()
	p.until("a ping is out", func() bool { return p.pings.Load() > sent })
	l.Event(pq.ListenerEventDisconnected) // lost while the ping is out
	if p.c.Listening() {
		t.Fatal("a disconnect did not suspend the cache")
	}
	p.mode.Store(0)
	p.gate <- nil // the ping answers: fine
	sent = p.pings.Load()
	time.Sleep(50 * time.Millisecond) // ten ping periods
	if p.c.Listening() {
		t.Fatal("a ping's answer resumed the cache after a disconnect")
	}
	if p.kept() {
		t.Fatal("the suspended cache answered from a kept entry")
	}
	if got := p.pings.Load(); got != sent {
		t.Errorf("%d pings were sent while nothing is kept", got-sent)
	}
	select {
	case <-l.Done:
		t.Fatal("the listener was let go though it only lost its connection")
	default:
	}
	// a failed attempt, then the real reconnect
	l.Event(pq.ListenerEventConnectionAttemptFailed)
	if p.c.Listening() {
		t.Fatal("a failed attempt resumed the cache")
	}
	l.Event(pq.ListenerEventReconnected)
	p.until("the reconnect resumes the cache", p.c.Listening)
	if !p.kept() {
		t.Error("the resumed cache keeps no entries")
	}
}
