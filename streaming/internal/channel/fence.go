package channel

import (
	"context"

	"github.com/google/uuid"
)

// Fence decides whether this host may run a channel (Epic 32.2, design §3.4): one
// encode per channel in the whole cluster, fenced by the placement's generation. The
// engine asks it before every station start, re-checks running stations every few
// seconds, and releases the placement when it reaps an idle station. Nil: no cluster
// (every channel may start here).
type Fence interface {
	// Allow grants a start: the placement names this host. The error is returned to
	// the caller (e.g. ErrRunningElsewhere, or a no-room error).
	Allow(ctx context.Context, channelID uuid.UUID) (Grant, error)
	// Check reports whether a station holding gen may keep running; newGen > 0 when a
	// newer placement names this host again (the station adopts it).
	Check(ctx context.Context, channelID uuid.UUID, gen int64) (keep bool, newGen int64)
	// Ack confirms the station for gen runs (placement state live).
	Ack(ctx context.Context, channelID uuid.UUID, gen int64)
	// Release gives the placement back (only while it still holds gen).
	Release(ctx context.Context, channelID uuid.UUID, gen int64)
}

// Grant is a Fence's permission to start a channel.
type Grant struct {
	// Gen is the placement generation the station holds.
	Gen int64
	// Discontinuity: the channel moves here from another host (failover, move); the
	// station's HLS starts with a discontinuity.
	Discontinuity bool
}

// StartOptions are passed to a StartFunc through its context.
type StartOptions struct {
	Discontinuity bool
}

type startOptsKey struct{}

// WithStartOptions attaches StartOptions to ctx.
func WithStartOptions(ctx context.Context, o StartOptions) context.Context {
	return context.WithValue(ctx, startOptsKey{}, o)
}

// StartOptionsFrom reads StartOptions from ctx.
func StartOptionsFrom(ctx context.Context) (StartOptions, bool) {
	o, ok := ctx.Value(startOptsKey{}).(StartOptions)
	return o, ok
}

type placementGenKey struct{}

// WithPlacementGen attaches the placement generation a (signed front-door) request
// carries.
func WithPlacementGen(ctx context.Context, gen int64) context.Context {
	return context.WithValue(ctx, placementGenKey{}, gen)
}

// PlacementGenFrom reads the generation WithPlacementGen attached (0: none).
func PlacementGenFrom(ctx context.Context) int64 {
	g, _ := ctx.Value(placementGenKey{}).(int64)
	return g
}
