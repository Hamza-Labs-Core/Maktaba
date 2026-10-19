package placement

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// SweepInterval is how often the home looks for hosts that went down.
const SweepInterval = 2 * time.Second

// Demand reports how many viewers a channel has on the front door (TS clients
// attached, HLS viewers within the warm grace, remote pushes); 0: none.
type Demand func(ch uuid.UUID) int

// Sweeper re-places the watched channels of hosts that went down (design §6.1):
// every SweepInterval it refreshes the hosts; for each host newly down, every active
// placement with demand is re-placed with reason failover (the most watched first),
// the others are released. With tv_cluster.failover off it only releases.
type Sweeper struct {
	Svc    *Service
	Demand Demand
	// Cleaner, when set, runs every minute (stale channel_runtime rows).
	Cleaner func(ctx context.Context)
}

// Run sweeps until ctx ends.
func (w *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	last := time.Time{}
	for {
		w.SweepOnce(ctx)
		if w.Cleaner != nil && time.Since(last) > time.Minute {
			w.Cleaner(ctx)
			last = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepOnce refreshes the hosts and handles those newly down. Returns the channels
// re-placed.
func (w *Sweeper) SweepOnce(ctx context.Context) []uuid.UUID {
	down := w.Svc.Hosts.Refresh(ctx)
	var moved []uuid.UUID
	for _, h := range down {
		moved = append(moved, w.failover(ctx, h)...)
	}
	return moved
}

func (w *Sweeper) failover(ctx context.Context, host string) []uuid.UUID {
	ps, err := w.Svc.Store.OnHost(ctx, host)
	if err != nil {
		return nil
	}
	type item struct {
		p Placement
		n int
	}
	items := make([]item, 0, len(ps))
	for _, p := range ps {
		n := 0
		if w.Demand != nil {
			n = w.Demand(p.Channel)
		}
		items = append(items, item{p, n})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].n > items[j].n })
	on := w.Svc.settings(ctx).Failover
	var moved []uuid.UUID
	for _, it := range items {
		w.Svc.Invalidate(it.p.Channel)
		if it.n == 0 || !on {
			_, _ = w.Svc.Store.Release(ctx, it.p.Channel, it.p.Gen)
			w.Svc.changed(it.p.Channel)
			continue
		}
		np, err := w.Svc.Place(ctx, it.p.Channel, ReasonFailover, "")
		if err != nil {
			w.Svc.log().Warn("tv: failover found no host", "channel_id", it.p.Channel.String(),
				"from", host, "err", err.Error(), "event", "tv_failover_failed")
			continue
		}
		w.Svc.log().Info("tv: channel failed over", "channel_id", it.p.Channel.String(), "from", host,
			"to", np.Host, "host_id", np.Host, "generation", np.Gen, "event", "tv_failover")
		tvmetrics.Failovers.Inc(host)
		moved = append(moved, it.p.Channel)
	}
	return moved
}
