// Package channels implements Story 27.1 — virtual channel CRUD:
//
//	GET    /api/channels            ?library_id= ?category= ?enabled=
//	POST   /api/channels
//	GET    /api/channels/{id}
//	PATCH  /api/channels/{id}
//	DELETE /api/channels/{id}
//	POST   /api/channels/reorder    [{id, number}] bulk renumber
//
// A channel is a programming rule over the library (Story 27.2 turns it
// into a schedule, Story 27.3 serves it live, Story 27.4 lists it in the
// guide). This package owns only the definition. Mirrors
// handlers/collections; admin-gated like the rest of the admin surface.
package channels

import (
	"encoding/json"
	"time"
)

// Mode constants — the closed vocabulary the slot-0081 CHECK enforces.
const (
	ModeShuffle  = "shuffle"
	ModeMarathon = "marathon"
	ModeSchedule = "schedule"
	ModeSmartMix = "smart_mix"
)

// Transition constants — the slot-0081 `transition` CHECK vocabulary.
const (
	TransitionCut       = "cut"
	TransitionCrossfade = "crossfade"
)

// Channel is the over-the-wire shape.
type Channel struct {
	ID           string          `json:"id"`
	LibraryID    *string         `json:"library_id,omitempty"`
	Number       int             `json:"number"`
	Name         string          `json:"name"`
	Slug         string          `json:"slug"`
	LogoPath     *string         `json:"logo_path,omitempty"`
	Category     string          `json:"category"`
	Mode         string          `json:"mode"`
	ModeConfig   json.RawMessage `json:"mode_config,omitempty"`
	SourceFilter json.RawMessage `json:"source_filter,omitempty"`
	Transition   string          `json:"transition"`
	Enabled      bool            `json:"enabled"`
	SortOrder    int             `json:"sort_order"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`

	// NowPlaying is a cheap join to the current channel_programs block
	// (Story 27.4's "now" query), attached on list/get. Nil when the
	// schedule has no current block (cold/empty channel) or the
	// channel_programs table isn't present yet.
	NowPlaying *NowPlaying `json:"now_playing,omitempty"`
}

// NowPlaying is the current-block summary shown on list/get.
type NowPlaying struct {
	Title    string    `json:"title"`
	StartAt  time.Time `json:"start_at"`
	EndAt    time.Time `json:"end_at"`
	Progress float64   `json:"progress"`
}

// CreateRequest is the POST body.
type CreateRequest struct {
	LibraryID    *string         `json:"library_id,omitempty"`
	Number       *int            `json:"number,omitempty"`
	Name         string          `json:"name"`
	Category     string          `json:"category,omitempty"`
	Mode         string          `json:"mode"`
	ModeConfig   json.RawMessage `json:"mode_config,omitempty"`
	SourceFilter json.RawMessage `json:"source_filter,omitempty"`
	Transition   string          `json:"transition,omitempty"`
	Enabled      *bool           `json:"enabled,omitempty"`
	SortOrder    *int            `json:"sort_order,omitempty"`
}

// PatchRequest is the PATCH body — all optional.
type PatchRequest struct {
	Number       *int            `json:"number,omitempty"`
	Name         *string         `json:"name,omitempty"`
	Category     *string         `json:"category,omitempty"`
	Mode         *string         `json:"mode,omitempty"`
	ModeConfig   json.RawMessage `json:"mode_config,omitempty"`
	SourceFilter json.RawMessage `json:"source_filter,omitempty"`
	Transition   *string         `json:"transition,omitempty"`
	Enabled      *bool           `json:"enabled,omitempty"`
	SortOrder    *int            `json:"sort_order,omitempty"`
}

// ReorderEntry is one element of the bulk-renumber POST body.
type ReorderEntry struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
}
