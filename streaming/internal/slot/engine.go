package slot

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
)

// The channel engine in a slot (design §2.3) is today's engine with a fence that
// also asks the slot's role and release, a channel_runtime that only the live
// slot writes (the row is the channel on air, one per channel), output under
// <cache>/tv/<slot>/ and an encoder budget from the front door.

// ErrNotRunnable: this slot may not run a station now (idle, draining, or bound
// to another release).
type ErrNotRunnable struct{ Why string }

func (e ErrNotRunnable) Error() string { return "slot: no station here (" + e.Why + ")" }

// Fence is a slot's station fence: the channel's placement names this host (the
// front door placed it; a slot never places), and the slot's role allows
// stations. Only the live slot acknowledges and releases placements: the hot
// standby's stations come and go with its warm set without touching them.
type Fence struct {
	Store placement.Store
	Self  string // the host id
	State *State
}

// Allow implements channel.Fence.
func (f Fence) Allow(ctx context.Context, ch uuid.UUID) (channel.Grant, error) {
	if ok, why := f.State.MayRun(); !ok {
		return channel.Grant{}, ErrNotRunnable{why}
	}
	p, found, err := f.Store.Get(ctx, ch)
	if err != nil {
		return channel.Grant{}, err
	}
	if !found || !p.Active() || p.Host != f.Self {
		return channel.Grant{}, placement.ErrNotPlacedHere
	}
	return channel.Grant{Gen: p.Gen, Discontinuity: p.Takeover()}, nil
}

// Check implements channel.Fence: keep while the placement names this host.
func (f Fence) Check(ctx context.Context, ch uuid.UUID, gen int64) (bool, int64) {
	if ok, _ := f.State.MayRun(); !ok {
		return false, 0
	}
	p, found, err := f.Store.Get(ctx, ch)
	if err != nil {
		return true, 0
	}
	if !found || !p.Active() || p.Host != f.Self {
		return false, 0
	}
	if p.Gen != gen {
		return true, p.Gen
	}
	return true, 0
}

// Ack implements channel.Fence (the live slot only).
func (f Fence) Ack(ctx context.Context, ch uuid.UUID, gen int64) {
	if f.State.Live() {
		_, _ = f.Store.Ack(ctx, ch, gen)
	}
}

// Release implements channel.Fence (the live slot only).
func (f Fence) Release(ctx context.Context, ch uuid.UUID, gen int64) {
	if f.State.Live() {
		_, _ = f.Store.Release(ctx, ch, gen)
	}
}

// Repo is the slot engine's repo: reads pass through; channel_runtime (one row
// per channel: the channel on air) is written by the live slot only.
type Repo struct {
	*channel.PGRepo
	State *State
}

// SetRuntime implements channel.Repo.
func (r Repo) SetRuntime(ctx context.Context, rt channel.Runtime) error {
	if !r.State.Live() {
		return nil
	}
	return r.PGRepo.SetRuntime(ctx, rt)
}

// ClearRuntime implements channel.Repo.
func (r Repo) ClearRuntime(ctx context.Context, id uuid.UUID) error {
	if !r.State.Live() {
		return nil
	}
	return r.PGRepo.ClearRuntime(ctx, id)
}

// ClearHostRuntime clears the host's rows at boot only when this slot is live
// (a restarting standby must not clear the live slot's rows).
func (r Repo) ClearHostRuntime(ctx context.Context, hostID, host string) (int64, error) {
	if !r.State.Live() {
		return 0, nil
	}
	return r.PGRepo.ClearHostRuntime(ctx, hostID, host)
}

// SetHostStatus is the front door's (the host heartbeat): a slot never writes it.
func (r Repo) SetHostStatus(context.Context, channel.HostStatus) error { return nil }

// Settings applies the slot's encoder budget over the host's output settings.
type Settings struct {
	channel.Settings
	State *State
}

// Output implements channel.Settings.
func (s Settings) Output(ctx context.Context) channel.OutputSettings {
	out := s.Settings.Output(ctx)
	b := s.State.Budget()
	if b.MaxActive > 0 {
		out.MaxActive = b.MaxActive
	}
	if b.NVENCMax > 0 {
		out.NVENCMax = b.NVENCMax
	}
	if b.VTMax > 0 {
		out.VTMax = b.VTMax
	}
	return out
}

// Strip passes the strip defaults through (channel.StripSource).
func (s Settings) Strip(ctx context.Context) channel.StripSettings {
	if ss, ok := s.Settings.(channel.StripSource); ok {
		return ss.Strip(ctx)
	}
	return channel.DefaultStripSettings()
}

// Layout puts a slot's HLS windows under <root>/tv/<slot>/ (AC6: no two slots
// share a directory).
type Layout struct {
	Root string
	Name string
}

// HLSDir implements channel.Layout.
func (l Layout) HLSDir(sessionID string) string {
	return filepath.Join(l.Root, "tv", l.Name, sessionID)
}

// IsNotRunnable reports a slot refusing stations.
func IsNotRunnable(err error) bool {
	var e ErrNotRunnable
	return errors.As(err, &e)
}
