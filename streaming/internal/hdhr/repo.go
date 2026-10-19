// Package hdhr implements Story 27.5 — HDHomeRun tuner emulation.
//
// It makes Maktaba's channels discoverable by Plex DVR / Jellyfin / Emby
// with zero config by speaking the SiliconDust HDHomeRun protocol: an
// SSDP responder on udp/1900, a small JSON discovery surface
// (/discover.json, /lineup.json, /lineup_status.json), and a continuous
// MPEG-TS stream per tuner connection (/auto/v{channel}). The "tuner" is
// virtual; its channels are Maktaba's, and its stream is the channel
// engine's MPEG-TS output (Story 27.3 D9) joined at the wall-clock
// offset.
package hdhr

import (
	"context"

	"github.com/google/uuid"
)

// DefaultTunerCount is a new device's tuner count. A tuner is one client
// pull (Plex recording or watching); clients of the same channel share its
// one encode, so the count is independent of how many channels the host
// encodes at once (MAKTABA_CHANNEL_MAX_ACTIVE / tv_output).
const DefaultTunerCount = 20

// Device is the singleton emulated tuner (slot 0084 hdhr_device).
type Device struct {
	DeviceID     string
	UUID         string
	FriendlyName string
	TunerCount   int
	Enabled      bool
}

// LineupChannel is one enabled channel as it appears in the HDHomeRun
// lineup.
type LineupChannel struct {
	ID     uuid.UUID
	Number int
	Name   string
	Slug   string
}

// Repo is the data surface the HDHomeRun handler needs.
type Repo interface {
	// Device loads (and lazily provisions) the singleton device row.
	Device(ctx context.Context) (Device, error)
	// Lineup returns the enabled channels in dial order.
	Lineup(ctx context.Context) ([]LineupChannel, error)
}
