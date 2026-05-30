package router

import (
	"context"
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws/eventbus"
)

// wireEventBus stands up the cross-replica WS event bus (Epic 19
// Story 19.2 / HLB-353) and attaches it to the WS handler, when a DSN
// + context are supplied. It is a no-op otherwise — the single-process
// hub path is unchanged, so dev/test deployments need no Postgres.
//
// Wiring:
//   - PostgresBackend over d.DB (table ops) + a dedicated pq.Listener
//     on 'ws.events' (slot-0061 trigger).
//   - Bus.Run: this replica's LISTEN loop, fanning every event out to
//     `hub` (the replica-local fan-out the SSE handler reads from).
//   - Bus.Pruner: 7-day retention sweep (Story 19.2 AC3).
//   - wsHandler.Replay: on-(re)connect catch-up via the bus.
//
// The Run/Pruner goroutines are bound to d.BusCtx so a graceful
// shutdown stops them.
func wireEventBus(d P6Deps, hub *ws.Hub, wsHandler *ws.Handler) {
	if d.BusCtx == nil || d.BusDSN == "" || d.DB == nil {
		return
	}
	backend := eventbus.NewPostgresBackend(d.DB, d.BusDSN, slog.Default())
	bus := eventbus.New(backend, hub, slog.Default())

	go func() {
		if err := bus.Run(d.BusCtx); err != nil && d.BusCtx.Err() == nil {
			slog.Default().Warn("eventbus: Run loop exited",
				"err", err, "event", "eventbus_run_exited")
		}
	}()
	go bus.Pruner(d.BusCtx, eventbus.DefaultRetention, eventbus.DefaultPruneEvery)

	wsHandler.Replay = busReplayAdapter{bus: bus}
}

// busReplayAdapter maps eventbus.Bus onto the ws.Replayer seam (ws
// deliberately has no hard dependency on eventbus to avoid an import
// cycle and keep the dev path bus-free).
type busReplayAdapter struct{ bus *eventbus.Bus }

func (a busReplayAdapter) Replay(ctx context.Context, channel string, lastEventID int64) ([]ws.ReplayEvent, error) {
	evs, err := a.bus.Replay(ctx, channel, lastEventID)
	if err != nil {
		return nil, err
	}
	out := make([]ws.ReplayEvent, 0, len(evs))
	for _, e := range evs {
		payload := map[string]any{}
		for k, v := range e.Payload {
			payload[k] = v
		}
		payload["_event_id"] = e.ID
		out = append(out, ws.ReplayEvent{Type: e.Type, At: e.At, Payload: payload})
	}
	return out, nil
}
