package queues

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// SnapshotChannel is the ephemeral SSE channel behind /ws/queues.
const SnapshotChannel = "queues"

// SnapshotEventType is the envelope type of a snapshot frame.
const SnapshotEventType = "queues.snapshot"

// DefaultSnapshotInterval is the publish cadence (plan-06-11 D14).
const DefaultSnapshotInterval = 2 * time.Second

// SnapshotEvent builds the ws.Event for a snapshot. Snapshots carry no
// "_event_id": they are ephemeral, with no durability or replay.
func SnapshotEvent(s Snapshot) (ws.Event, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return ws.Event{}, err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(b, &payload); err != nil {
		return ws.Event{}, err
	}
	return ws.Event{Type: SnapshotEventType, At: s.At, Payload: payload}, nil
}

// InitialFrame is the ws.Handler.Initial hook: the first frame a new
// /ws/queues client receives, so it never waits for the next tick.
func (h *Handler) InitialFrame(ctx context.Context, channel string) *ws.Event {
	if channel != SnapshotChannel {
		return nil
	}
	snap, err := h.BuildSnapshot(ctx)
	if err != nil {
		slog.Default().Warn("queues: initial snapshot failed", "err", err, "event", "queues_snapshot_failed")
		return nil
	}
	ev, err := SnapshotEvent(scopeSnapshot(principal.FromContext(ctx), snap))
	if err != nil {
		return nil
	}
	return &ev
}

// FilterEvent is the ws.Handler.Filter hook for the queue channels: a
// caller without every library gets the /ws/queues snapshot without the
// running job rows of other libraries, and on /ws/jobs only the events
// of jobs in its libraries (an event without a library_id is dropped).
func FilterEvent(p *principal.Principal, channel string, e ws.Event) (ws.Event, bool) {
	if fullView(p) {
		return e, true
	}
	switch channel {
	case SnapshotChannel:
		if e.Type != SnapshotEventType || e.Payload == nil {
			return e, true
		}
		qs, _ := e.Payload["queues"].([]any)
		out := make([]any, 0, len(qs))
		for _, q := range qs {
			m, ok := q.(map[string]any)
			if !ok {
				continue
			}
			c := make(map[string]any, len(m))
			for k, v := range m {
				c[k] = v
			}
			if refs, ok := m["running_jobs"].([]any); ok {
				kept := make([]any, 0, len(refs))
				for _, ref := range refs {
					if rm, ok := ref.(map[string]any); ok && payloadLibraryVisible(p, rm) {
						kept = append(kept, ref)
					}
				}
				c["running_jobs"] = kept
			}
			out = append(out, c)
		}
		payload := make(map[string]any, len(e.Payload))
		for k, v := range e.Payload {
			payload[k] = v
		}
		payload["queues"] = out
		e.Payload = payload
		return e, true
	case "jobs":
		// job.* events carry the job ({"job": JobRef}) or its library_id;
		// queue.control / pool.updated carry neither and are global
		if job, ok := e.Payload["job"].(map[string]any); ok {
			return e, payloadLibraryVisible(p, job)
		}
		if _, ok := e.Payload["library_id"]; ok {
			return e, payloadLibraryVisible(p, e.Payload)
		}
		if _, ok := e.Payload["job_id"]; ok {
			return e, false
		}
		return e, true
	}
	return e, true
}

// payloadLibraryVisible reports whether p may see an event payload by its
// library_id (absent or null: a full view only).
func payloadLibraryVisible(p *principal.Principal, m map[string]any) bool {
	lib, _ := m["library_id"].(string)
	if lib == "" {
		return fullView(p)
	}
	return canReadLibrary(p, lib)
}

// PublishSnapshots publishes a snapshot to the hub every interval, but
// only while at least one client is subscribed to the channel. It
// blocks until ctx is done.
func (h *Handler) PublishSnapshots(ctx context.Context, interval time.Duration) {
	if h.Hub == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultSnapshotInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.publishSnapshotOnce(ctx)
		}
	}
}

// publishSnapshotOnce is one tick; it reports whether it published.
func (h *Handler) publishSnapshotOnce(ctx context.Context) bool {
	if h.Hub.SubscriberCount(SnapshotChannel) == 0 {
		return false
	}
	snap, err := h.BuildSnapshot(ctx)
	if err != nil {
		slog.Default().Warn("queues: snapshot failed", "err", err, "event", "queues_snapshot_failed")
		return false
	}
	ev, err := SnapshotEvent(snap)
	if err != nil {
		return false
	}
	h.Hub.Publish(SnapshotChannel, ev)
	h.recordBacklog(ctx, snap)
	return true
}

// recordBacklog persists the ready backlog as health metric
// "backlog.ready" (host "api"), at most one sample per minute, so the
// health chart has a backlog series: workers do not sample it. Only the
// snapshot publisher calls this, so GET handlers never write.
func (h *Handler) recordBacklog(ctx context.Context, snap Snapshot) {
	_, err := h.DB.ExecContext(ctx, `
		INSERT INTO health_samples (at, host, metric, value)
		VALUES (date_trunc('minute', $1::timestamptz), 'api', 'backlog.ready', $2)
		ON CONFLICT DO NOTHING`, snap.At, float64(snap.Health.Backlog.Ready))
	if err != nil {
		slog.Default().Debug("queues: backlog sample not recorded", "err", err)
	}
}
