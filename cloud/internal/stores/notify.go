package stores

import (
	"context"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// The cloud database's change notifications (Epic 50 story 50.05). The
// triggers of cloud migration 00500003 send them on every change to a home.
const (
	ChannelTierChanged   = "tier_changed"   // payload: the slug whose plan changed
	ChannelServerChanged = "server_changed" // payload: the slug that changed or went
)

// Listen calls on for each notification on channels until ctx ends,
// reconnecting by itself (lib/pq's Listener). After a reconnect it calls
// missed (notifications may have been lost while it was away).
func Listen(ctx context.Context, dsn string, channels []string, logger *slog.Logger, on func(channel, payload string), missed func()) error {
	if logger == nil {
		logger = slog.Default()
	}
	l := pq.NewListener(dsn, time.Second, 30*time.Second, func(ev pq.ListenerEventType, err error) {
		switch ev {
		case pq.ListenerEventReconnected:
			logger.Info("stores: the change listener reconnected", "event", "db_listener_reconnected")
			if missed != nil {
				missed()
			}
		case pq.ListenerEventConnectionAttemptFailed, pq.ListenerEventDisconnected:
			logger.Warn("stores: the change listener lost the database", "err", err, "event", "db_listener_down")
		}
	})
	for _, c := range channels {
		if err := l.Listen(c); err != nil {
			_ = l.Close()
			return err
		}
	}
	go func() {
		defer func() { _ = l.Close() }()
		ping := time.NewTicker(90 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case n := <-l.Notify:
				if n == nil { // reconnected: the callback above already ran
					continue
				}
				on(n.Channel, n.Extra)
			case <-ping.C:
				go func() { _ = l.Ping() }()
			}
		}
	}()
	return nil
}
