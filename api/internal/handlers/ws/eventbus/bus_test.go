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
	remaining, _ := backend.Replay(ctx, "jobs", 0, 0)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 row to survive prune, got %d", len(remaining))
	}
}

// TestReconnectTriggersReplayOfMissedEvents proves Fix #1: when a
// replica's listener "reconnects" (pq.Listener re-establishes its
// connection after a DB blip), the Bus re-scans EVERY channel it has a
// non-zero cursor for and delivers everything missed during the
// outage — WITHOUT waiting for a new per-channel publish. Before the
// fix the reconnect signal was a no-op so a low-traffic channel
// stalled until the next NOTIFY (unbounded latency).
func TestReconnectTriggersReplayOfMissedEvents(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hub := ws.NewHub()
	bus := New(backend, hub, nil)
	bus.rescanEvery = time.Hour // isolate: only the reconnect path may deliver

	sub := hub.Subscribe("jobs")
	go func() { _ = bus.Run(ctx) }()
	waitListeners(t, backend, 1)

	// One event delivered normally so the replica has a non-zero
	// cursor for "jobs" (id 1).
	if err := bus.Publish(ctx, "jobs", "jobs.progress", map[string]any{"n": 1}); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if e := recv(t, sub, 2*time.Second); e.Payload["n"] != float64(1) && e.Payload["n"] != 1 {
		t.Fatalf("expected event 1 first, got %v", e.Payload)
	}

	// Listener is "down": the rows are durably appended on the
	// primary but NO NOTIFY reaches this replica (DB blip) — exactly
	// the gap Fix #1's reconnect re-scan must heal.
	backend.SetListenerDown(true)
	for _, n := range []int{2, 3, 4} {
		if _, err := backend.Append(ctx, Event{Channel: "jobs", Type: "jobs.progress", Payload: map[string]any{"n": n}}); err != nil {
			t.Fatalf("append %d: %v", n, err)
		}
	}
	// No NOTIFY arrives, no new publish. Nothing should be delivered.
	select {
	case e := <-sub.C:
		t.Fatalf("event delivered before reconnect (no NOTIFY expected): %v", e.Payload)
	case <-time.After(150 * time.Millisecond):
	}

	// Listener reconnects. This MUST re-scan "jobs" and deliver 2,3,4.
	backend.SignalReconnect()

	seen := map[float64]bool{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 3 {
		select {
		case e := <-sub.C:
			seen[asF(e.Payload["n"])] = true
		case <-deadline:
			t.Fatalf("reconnect did not replay missed events; saw %v (want 2,3,4)", seen)
		}
	}
	if !seen[2] || !seen[3] || !seen[4] {
		t.Fatalf("reconnect replay incomplete: %v", seen)
	}
}

// TestPeriodicSafetyRescanDeliversSilentlyMissedEvents proves Fix #1's
// belt-and-braces tick: an event whose NOTIFY was silently lost (no
// overflow signal, no reconnect, no subsequent publish) is still
// delivered by the periodic re-scan. Before the fix there was no
// independent tick so it stalled forever.
func TestPeriodicSafetyRescanDeliversSilentlyMissedEvents(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hub := ws.NewHub()
	bus := New(backend, hub, nil)
	bus.rescanEvery = 10 * time.Millisecond // fast tick for the test

	sub := hub.Subscribe("jobs")
	go func() { _ = bus.Run(ctx) }()
	waitListeners(t, backend, 1)

	// Establish a non-zero cursor for "jobs" via a delivered event.
	if err := bus.Publish(ctx, "jobs", "jobs.progress", map[string]any{"n": 1}); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if e := recv(t, sub, 2*time.Second); asF(e.Payload["n"]) != 1 {
		t.Fatalf("expected event 1, got %v", e.Payload)
	}

	// Silently-missed event: appended durably, NOTIFY lost, and NO
	// reconnect signal — only the independent periodic tick can
	// recover it (the listener stays "down" the whole time).
	backend.SetListenerDown(true)
	if _, err := backend.Append(ctx, Event{Channel: "jobs", Type: "jobs.progress", Payload: map[string]any{"n": 2}}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	// Only the periodic tick can deliver this.
	got := recv(t, sub, 2*time.Second)
	if asF(got.Payload["n"]) != 2 {
		t.Fatalf("periodic re-scan did not deliver the silently-missed event; got %v", got.Payload)
	}
}

// TestReplayIsChunkedAndBounded proves Fix #2: a backlog larger than
// the replay bound is delivered fully and in order via multiple
// bounded chunks, and a single Replay call never returns more than
// replayLimit rows (so Bus.Run is never blocked materializing an
// unbounded tail after a long outage).
func TestReplayIsChunkedAndBounded(t *testing.T) {
	backend := NewMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hub := ws.NewHub()
	bus := New(backend, hub, nil)
	bus.replayLimit = 4 // tiny bound to force chunking deterministically
	bus.rescanEvery = time.Hour

	const total = 13 // > 3 chunks at limit 4
	for i := 1; i <= total; i++ {
		if _, err := backend.Append(ctx, Event{Channel: "jobs", Type: "jobs.progress", Payload: map[string]any{"n": i}}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Replay (the bounded backend query) must honor the LIMIT.
	page, err := backend.Replay(ctx, "jobs", 0, bus.replayLimit)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(page) != bus.replayLimit {
		t.Fatalf("Replay ignored LIMIT: got %d rows, want %d", len(page), bus.replayLimit)
	}
	if page[0].ID != 1 || page[len(page)-1].ID != 4 {
		t.Fatalf("Replay not ordered/bounded from cursor: %+v", page)
	}

	// deliverThrough must chunk through the whole backlog in order.
	sub := hub.Subscribe("jobs")
	go func() { _ = bus.Run(ctx) }()
	waitListeners(t, backend, 1)
	// A single NOTIFY for the highest id triggers the chunked drain.
	if _, err := backend.Append(ctx, Event{Channel: "jobs", Type: "jobs.progress", Payload: map[string]any{"n": total + 1}}); err != nil {
		t.Fatalf("append trigger: %v", err)
	}
	backend.SignalReconnect() // force a deterministic re-scan now

	var order []int
	deadline := time.After(3 * time.Second)
	for len(order) < total+1 {
		select {
		case e := <-sub.C:
			order = append(order, int(asF(e.Payload["n"])))
		case <-deadline:
			t.Fatalf("chunked replay incomplete: delivered %d/%d: %v", len(order), total+1, order)
		}
	}
	for i, v := range order {
		if v != i+1 {
			t.Fatalf("chunked replay out of order at %d: %v", i, order)
		}
	}
}

// asF normalizes a JSON-ish numeric payload value to float64.
func asF(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return -1
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
