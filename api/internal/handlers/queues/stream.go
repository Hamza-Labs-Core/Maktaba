package queues

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

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
func SnapshotEvent(s QueuesSnapshot) (ws.Event, error) {
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
	ev, err := SnapshotEvent(snap)
	if err != nil {
		return nil
	}
	return &ev
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
func (h *Handler) recordBacklog(ctx context.Context, snap QueuesSnapshot) {
	_, err := h.DB.ExecContext(ctx, `
		INSERT INTO health_samples (at, host, metric, value)
		VALUES (date_trunc('minute', $1::timestamptz), 'api', 'backlog.ready', $2)
		ON CONFLICT DO NOTHING`, snap.At, float64(snap.Health.Backlog.Ready))
	if err != nil {
		slog.Default().Debug("queues: backlog sample not recorded", "err", err)
	}
}
