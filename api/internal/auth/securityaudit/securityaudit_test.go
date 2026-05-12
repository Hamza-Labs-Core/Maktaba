package securityaudit

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEventsAreCanonical(t *testing.T) {
	good := []Event{
		EventLoginSuccess, EventLoginFailed, EventLogout, EventLogoutAll,
		EventLockoutUsername, EventLockoutIP, EventRefreshReplay,
		EventPasswordChanged, EventKeyRotated, EventAdminTokenUsed,
		EventPermissionDenied, EventStreamingDirect,
		EventPairCodeIssued, EventPairCodeClaimed,
		EventSessionRevoked, EventRefreshRevoked,
	}
	for _, e := range good {
		if !EventsAreCanonical(string(e)) {
			t.Errorf("expected canonical: %q", e)
		}
	}
	bad := []string{"", "login", "permission_denied", "logout.all", "made-up"}
	for _, s := range bad {
		if EventsAreCanonical(s) {
			t.Errorf("expected NOT canonical: %q", s)
		}
	}
}

func TestMarshalPayload_Default(t *testing.T) {
	out, err := marshalPayload(nil)
	if err != nil || out != "{}" {
		t.Errorf("nil: out=%q err=%v", out, err)
	}
	out, err = marshalPayload(map[string]any{})
	if err != nil || out != "{}" {
		t.Errorf("empty: out=%q err=%v", out, err)
	}
	out, err = marshalPayload(map[string]any{"k": 1})
	if err != nil || !strings.Contains(out, "\"k\":1") {
		t.Errorf("k: out=%q err=%v", out, err)
	}
}

func TestMarshalPayload_TooLarge(t *testing.T) {
	big := map[string]any{"k": strings.Repeat("x", 32*1024)}
	if _, err := marshalPayload(big); err == nil {
		t.Error("expected error on oversized payload")
	}
}

func TestWriteSampled_Dedupe(t *testing.T) {
	// Use a Writer with no DB — set the entry handler to track inserts.
	w := &Writer{last: map[string]time.Time{}}
	calls := 0
	w.writeFn = func(_ context.Context, _ Entry) error {
		calls++
		return nil
	}
	ctx := context.Background()
	entry := Entry{Event: EventAdminTokenUsed, ActorUserID: "u1"}
	for range 5 {
		_, _ = w.WriteSampled(ctx, entry, time.Minute)
	}
	if calls != 1 {
		t.Errorf("expected 1 call within window, got %d", calls)
	}
	// Push the recorded time backward beyond the window.
	w.mu.Lock()
	for k := range w.last {
		w.last[k] = time.Now().Add(-2 * time.Minute)
	}
	w.mu.Unlock()
	_, _ = w.WriteSampled(ctx, entry, time.Minute)
	if calls != 2 {
		t.Errorf("expected 2 calls after window expiry, got %d", calls)
	}
}

func TestWriteSampled_ZeroWindow(t *testing.T) {
	w := &Writer{last: map[string]time.Time{}}
	calls := 0
	w.writeFn = func(_ context.Context, _ Entry) error {
		calls++
		return nil
	}
	for range 3 {
		_, _ = w.WriteSampled(context.Background(), Entry{Event: EventLogout}, 0)
	}
	if calls != 3 {
		t.Errorf("zero window must always write: got %d", calls)
	}
}

func TestWrite_EmptyEvent(t *testing.T) {
	w := &Writer{}
	if err := w.Write(context.Background(), Entry{}); err == nil {
		t.Error("expected error on empty Event")
	} else if !strings.Contains(err.Error(), "empty event") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestWriter_ConcurrentDedupe(t *testing.T) {
	w := &Writer{last: map[string]time.Time{}}
	var mu sync.Mutex
	calls := 0
	w.writeFn = func(_ context.Context, _ Entry) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.WriteSampled(context.Background(), Entry{Event: EventLoginFailed, ActorUserID: "x"}, time.Minute)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("expected exactly 1 effective write under contention; got %d", calls)
	}
}

