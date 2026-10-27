package slot

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

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
	if f.staging() && (!found || !p.Active()) {
		// story 33.5: staging airs its release's channels for verification and
		// preview whether or not live has them placed now (an idle channel, a
		// channel the change adds); it never places one itself
		return channel.Grant{}, nil
	}
	if !found || !p.Active() || p.Host != f.Self {
		return channel.Grant{}, placement.ErrNotPlacedHere
	}
	return channel.Grant{Gen: p.Gen, Discontinuity: p.Takeover()}, nil
}

// staging reports the staging role (its stations follow the front door's warm
// set, not the live placements).
func (f Fence) staging() bool {
	r, _ := f.State.Role()
	return r == RoleStaging
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
	if f.staging() {
		// kept while no other host has it placed (its warm hold decides)
		return !found || !p.Active() || p.Host == f.Self, 0
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
		_, err := f.Store.Ack(ctx, ch, gen)
		placement.LogFenceWrite("ack", ch, gen, err)
	}
}

// Release implements channel.Fence (the live slot only).
func (f Fence) Release(ctx context.Context, ch uuid.UUID, gen int64) {
	if f.State.Live() {
		_, err := f.Store.Release(ctx, ch, gen)
		placement.LogFenceWrite("release", ch, gen, err)
	}
}

// Repo is the slot engine's repo: reads come from Reads (the slot's TV release,
// channel.ReleaseRepo, when it is bound to one; else the PGRepo's live view);
// channel_runtime (one row per channel: the channel on air) is written by the
// live slot only.
type Repo struct {
	*channel.PGRepo
	State *State
	Reads channel.Repo
	// Bound, when set and bound, is the release the slot was pinned to after its
	// start (it wins over Reads).
	Bound *Binding
}

func (r Repo) reads() channel.Repo {
	if r.Bound != nil {
		if b := r.Bound.Repo(); b != nil {
			return b
		}
	}
	return r.Reads
}

// ProgramsFrom implements channel.Repo (the release's generation when bound).
func (r Repo) ProgramsFrom(ctx context.Context, id uuid.UUID, from time.Time, limit int) ([]channel.ProgramBlock, error) {
	if rr := r.reads(); rr != nil {
		return rr.ProgramsFrom(ctx, id, from, limit)
	}
	return r.PGRepo.ProgramsFrom(ctx, id, from, limit)
}

// Look implements channel.Repo (the release's definition when bound).
func (r Repo) Look(ctx context.Context, id uuid.UUID) (channel.Look, error) {
	if rr := r.reads(); rr != nil {
		return rr.Look(ctx, id)
	}
	return r.PGRepo.Look(ctx, id)
}

// FillersAround implements channel.FillerSource (the release's generation when
// bound).
func (r Repo) FillersAround(ctx context.Context, id uuid.UUID, at time.Time, limit int) ([]channel.ProgramBlock, error) {
	if rr := r.reads(); rr != nil {
		if fs, ok := rr.(channel.FillerSource); ok {
			return fs.FillersAround(ctx, id, at, limit)
		}
		return nil, nil
	}
	return r.PGRepo.FillersAround(ctx, id, at, limit)
}

// ReportUnreadable reports a file only from the live slot: a standby or a
// staging slot airs the same files and would report them again.
func (r Repo) ReportUnreadable(ctx context.Context, videoID uuid.UUID, why string) error {
	if !r.State.Live() {
		return nil
	}
	return r.PGRepo.ReportUnreadable(ctx, videoID, why)
}

// Binding is the TV release a slot airs when it is pinned to one after its
// start (story 33.7): an unbound slot airs the live generation, which the swap
// commit flips — so before a swap the front door pins the live slot and the
// hot standby to the live release, and they keep airing it while the new
// release takes over channel by channel (and as the rollback target after).
type Binding struct {
	mu   sync.RWMutex
	repo channel.Repo
	raw  func(section string) json.RawMessage
}

// Set binds the slot to a release's repo and settings.
func (b *Binding) Set(repo channel.Repo, raw func(section string) json.RawMessage) {
	b.mu.Lock()
	b.repo, b.raw = repo, raw
	b.mu.Unlock()
}

// Repo is the bound release's repo (nil: unbound).
func (b *Binding) Repo() channel.Repo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.repo
}

// Raw is the bound release's settings (nil: unbound).
func (b *Binding) Raw() func(section string) json.RawMessage {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.raw
}

// BoundSettings are the bound release's settings once bound, else Base's.
type BoundSettings struct {
	Base  channel.Settings
	Bound *Binding
}

func (s BoundSettings) release() (ReleaseSettings, bool) {
	if s.Bound != nil {
		if raw := s.Bound.Raw(); raw != nil {
			return ReleaseSettings{Raw: raw}, true
		}
	}
	return ReleaseSettings{}, false
}

// TV implements channel.Settings.
func (s BoundSettings) TV(ctx context.Context) channel.TVSettings {
	if r, ok := s.release(); ok {
		return r.TV(ctx)
	}
	return s.Base.TV(ctx)
}

// Output implements channel.Settings.
func (s BoundSettings) Output(ctx context.Context) channel.OutputSettings {
	if r, ok := s.release(); ok {
		return r.Output(ctx)
	}
	return s.Base.Output(ctx)
}

// Strip implements channel.StripSource.
func (s BoundSettings) Strip(ctx context.Context) channel.StripSettings {
	if r, ok := s.release(); ok {
		return r.Strip(ctx)
	}
	if ss, ok := s.Base.(channel.StripSource); ok {
		return ss.Strip(ctx)
	}
	return channel.DefaultStripSettings()
}

// ReleaseSettings are a release's rules (tv, tv_output, tv_strip) over the
// host's env fallbacks (story 33.2: a slot never reads app_settings for what it
// airs). Raw returns a section's stored overrides.
type ReleaseSettings struct {
	Raw func(section string) json.RawMessage
}

// TV implements channel.Settings.
func (s ReleaseSettings) TV(context.Context) channel.TVSettings {
	tv, _ := channel.ApplyTVRules(channel.TVSettingsFromEnv(), s.Raw("tv"))
	return tv
}

// Output implements channel.Settings.
func (s ReleaseSettings) Output(context.Context) channel.OutputSettings {
	out, _ := channel.ApplyOutputRules(channel.OutputSettingsFromEnv(), s.Raw("tv_output"))
	return out
}

// Strip implements channel.StripSource.
func (s ReleaseSettings) Strip(context.Context) channel.StripSettings {
	st, _ := channel.ApplyStripRules(channel.DefaultStripSettings(), s.Raw("tv_strip"), false)
	return st
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

// Output implements channel.Settings. Until the front door's first budget
// (a slot just started or restarted), the hardware caps are the share VOD can
// never take (it holds at most half of them): the slot cannot know yet how
// many a transcode holds.
func (s Settings) Output(ctx context.Context) channel.OutputSettings {
	out := s.Settings.Output(ctx)
	if !s.State.Warmed() {
		out.NVENCMax -= out.NVENCMax / 2
		out.VTMax -= out.VTMax / 2
	}
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
