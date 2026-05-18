package eventbus

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// recv waits for one event on a hub subscriber or fails the test.
func recv(t *testing.T, s *ws.Subscriber, within time.Duration) ws.Event {
	t.Helper()
	select {
	case e := <-s.C:
		return e
	case <-time.After(within):
		t.Fatalf("timed out after %s waiting for event", within)
		return ws.Event{}
	}
}

// TestCrossReplica_PublishOnA_DeliveredToClientOnB is the keystone
// Story 19.2 AC2 proof. Two Bus instances over ONE shared backend ==
// two API replicas pointed at one Postgres. A WS client's socket lives
// on replica B's hub; the event is published on replica A. With the
// in-memory single-process hub this delivery is IMPOSSIBLE — it only
// works because the event goes through the durable table + NOTIFY and
// replica B's LISTEN loop fans it out to its own hub.
func TestCrossReplica_PublishOnA_DeliveredToClientOnB(t *testing.T) {
	backend := NewMemoryBackend() // the shared "Postgres"
	t.Cleanup(func() { _ = backend.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hubA := ws.NewHub()
	hubB := ws.NewHub()
	busA := New(backend, hubA, nil)
	busB := New(backend, hubB, nil)

	// Both replicas run their LISTEN loop.
	go func() { _ = busA.Run(ctx) }()
	go func() { _ = busB.Run(ctx) }()
	// Let both listeners register before publishing.
	waitListeners(t, backend, 2)

	// Client connects to replica B and subscribes to "jobs".
	clientOnB := hubB.Subscribe("jobs")

	// Event is published on replica A.
	if err := busA.Publish(ctx, "jobs", "jobs.progress", map[string]any{"video_id": "v1", "pct": 42}); err != nil {
		t.Fatalf("publish on A: %v", err)
	}

	got := recv(t, clientOnB, 2*time.Second)
	if got.Type != "jobs.progress" {
		t.Fatalf("client on B got wrong type: %q", got.Type)
	}
	if got.Payload["video_id"] != "v1" {
		t.Fatalf("client on B got wrong payload: %v", got.Payload)
	}
	// The monotonic cursor must be threaded through for reconnect.
	if id, ok := got.Payload["_event_id"]; !ok || id == int64(0) {
		t.Fatalf("event missing/zero _event_id cursor: %v", got.Payload)
	}
}

// TestCrossReplica_ChannelIsolation: an event on library:1 must not
// reach a client subscribed to jobs on the other replica.
func TestCrossReplica_ChannelIsolation(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hubA, hubB := ws.NewHub(), ws.NewHub()
	busA := New(backend, hubA, nil)
	busB := New(backend, hubB, nil)
	go func() { _ = busA.Run(ctx) }()
	go func() { _ = busB.Run(ctx) }()
	waitListeners(t, backend, 2)

	jobsClientOnB := hubB.Subscribe("jobs")
	if err := busA.Publish(ctx, "library:1", "library.video_added", map[string]any{"id": "x"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case e := <-jobsClientOnB.C:
		t.Fatalf("jobs subscriber leaked a library event: %v", e)
	case <-time.After(300 * time.Millisecond):
		// correct: no cross-channel delivery
	}
}

// TestReplay_RecoversMissedEventsAcrossReconnect is the Story 19.2
// AC3 proof: a client that processed up to event id N reconnects
// (possibly to a different replica) and gets exactly the events with
// id > N from the durable table, in order — nothing lost, nothing
// before N replayed.
func TestReplay_RecoversMissedEventsAcrossReconnect(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	ctx := context.Background()

	bus := New(backend, ws.NewHub(), nil)

	// Three events published while the client is connected.
	for _, p := range []map[string]any{{"n": 1}, {"n": 2}, {"n": 3}} {
		if err := bus.Publish(ctx, "jobs", "jobs.progress", p); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	// Client processed up to id 1, then dropped. It reconnects (to
	// ANY replica — Replay reads the shared table) with last_event_id=1.
	missed, err := bus.Replay(ctx, "jobs", 1)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(missed) != 2 {
		t.Fatalf("expected 2 missed events (ids 2,3), got %d: %v", len(missed), missed)
	}
	if missed[0].ID != 2 || missed[1].ID != 3 {
		t.Fatalf("replay not ordered/correct: %+v", missed)
	}
	if missed[0].Payload["n"] != float64(2) && missed[0].Payload["n"] != 2 {
		t.Fatalf("replayed payload wrong: %v", missed[0].Payload)
	}
}

// TestGapRecovery_DroppedNotifyStillDelivered proves the LISTEN loop
// self-heals: even if a NOTIFY is dropped (queue overflow), the next
// NOTIFY's replay-from-lastID scan delivers the skipped event, so no
// event is permanently lost on the live path.
func TestGapRecovery_DroppedNotifyStillDelivered(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hub := ws.NewHub()
	bus := New(backend, hub, nil)

	// Append two events with NO listener registered yet — both
	// NOTIFYs are "dropped" (no replica was listening). The durable
	// rows still exist.
	if _, err := backend.Append(ctx, Event{Channel: "jobs", Type: "jobs.progress", Payload: map[string]any{"n": 1}}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := backend.Append(ctx, Event{Channel: "jobs", Type: "jobs.progress", Payload: map[string]any{"n": 2}}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	sub := hub.Subscribe("jobs")
	go func() { _ = bus.Run(ctx) }()
	waitListeners(t, backend, 1)

	// A third event fires a NOTIFY the loop DOES receive. Its
	// gap-recovery scan (replay from lastID=0) must deliver 1, 2 AND 3.
	if err := bus.Publish(ctx, "jobs", "jobs.progress", map[string]any{"n": 3}); err != nil {
		t.Fatalf("publish 3: %v", err)
	}

	seen := map[float64]bool{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 3 {
		select {
		case e := <-sub.C:
			if n, ok := e.Payload["n"].(float64); ok {
				seen[n] = true
			} else if ni, ok := e.Payload["n"].(int); ok {
				seen[float64(ni)] = true
			}
		case <-deadline:
			t.Fatalf("gap recovery failed; only delivered %v (want 1,2,3)", seen)
		}
	}
}

// TestPrune_DropsExpiredKeepsRecent verifies the 7-day retention
// sweep (Story 19.2 AC3).
func TestPrune_DropsExpiredKeepsRecent(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	ctx := context.Background()

	old := Event{Channel: "jobs", Type: "x", At: time.Now().Add(-10 * 24 * time.Hour)}
	if _, err := backend.Append(ctx, old); err != nil {
		t.Fatalf("append old: %v", err)
	}
	fresh := Event{Channel: "jobs", Type: "x", At: time.Now()}
	if _, err := backend.Append(ctx, fresh); err != nil {
		t.Fatalf("append fresh: %v", err)
	}

	n, err := backend.Prune(ctx, time.Now().Add(-DefaultRetention))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned (the 10-day-old row), got %d", n)
	}
	remaining, _ := backend.Replay(ctx, "jobs", 0)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 row to survive prune, got %d", len(remaining))
	}
}

// waitListeners blocks until backend has at least n registered
// LISTEN streams (so a publish isn't raced against listener setup).
func waitListeners(t *testing.T, b *MemoryBackend, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		b.mu.Lock()
		got := len(b.listeners)
		b.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("only %d/%d listeners registered", got, n)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
