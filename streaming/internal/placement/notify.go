package placement

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Listen runs `LISTEN tv_placement` on a dedicated connection and calls fn with each
// changed channel ("<channel>:<host>:<generation>"), reconnecting on failure, until
// ctx ends.
func Listen(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, fn func(ch uuid.UUID)) {
	if logger == nil {
		logger = slog.Default()
	}
	backoff := time.Second
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, fn)
		if ctx.Err() != nil {
			return
		}
		logger.Warn("tv: placement notifications lost; reconnecting", "err", errString(err), "event", "tv_listen_failed")
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, fn func(ch uuid.UUID)) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN tv_placement`); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			// the connection's state is unknown after an interrupted wait
			_ = conn.Conn().Close(context.Background())
			return err
		}
		id, _, _ := strings.Cut(n.Payload, ":")
		if ch, err := uuid.Parse(id); err == nil {
			fn(ch)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
