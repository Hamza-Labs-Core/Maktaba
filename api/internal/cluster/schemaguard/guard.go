// Package schemaguard is a full node's API schema guard (Epic 35 story 35.8, design
// §9.3): the same rule, table and reason texts as the pipeline's cluster/schema.py and
// the streaming node's streaming/internal/cluster/schema.go (Epic 31 design §5.1).
//
// A node's code is written against the migrations it ships (SchemaRev); the home
// migrates its own database, a node never does. A home whose applied version is below
// SchemaRev, or whose maktaba_min_node_schema() names a newer schema than SchemaRev,
// pauses the node: its `any` routes answer 503 {code: "paused", reason}, the web shows
// the paused page, `local` routes keep working and `home` routes are still proxied (the
// home's API is the newer side). A home whose version cannot be read is not judged.
package schemaguard

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// SchemaRev is the highest migration slot this code ships (shared/db/migrations); a
// test fails when a migration is added without raising it.
const SchemaRev = 143

// MinNodeFunction is what a migration that breaks older nodes (re)defines.
const MinNodeFunction = "maktaba_min_node_schema"

// Intervals of design §9.3: every 5 minutes, every 30 s while paused.
const (
	CheckEvery       = 5 * time.Minute
	CheckEveryPaused = 30 * time.Second
)

// HomeSchema is what the home's database says about its schema.
type HomeSchema struct {
	Applied    int  `json:"applied"`
	Known      bool `json:"known"`
	MinNode    int  `json:"min_node"`
	HasMinNode bool `json:"has_min_node"`
}

// Read reads the applied version and the breaking declaration (the node role reads
// goose's table: slot 0103 granted every table of the schema).
func Read(ctx context.Context, db *sql.DB) HomeSchema {
	var h HomeSchema
	var applied int64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&applied); err == nil {
		h.Applied, h.Known = int(applied), true
	}
	var has bool
	if err := db.QueryRowContext(ctx, `SELECT to_regproc('`+MinNodeFunction+`') IS NOT NULL`).Scan(&has); err == nil && has {
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT `+MinNodeFunction+`()`).Scan(&n); err == nil {
			h.MinNode, h.HasMinNode = int(n), true
		}
	}
	return h
}

// PauseReason is "paused: <why>" when a node with schema `required` must not run
// against h, else "". The words match the pipeline's and the streaming node's.
func PauseReason(h HomeSchema, required int) string {
	if h.Known && h.Applied < required {
		return fmt.Sprintf("paused: home database at %04d, this machine needs %04d — update the home",
			h.Applied, required)
	}
	if h.HasMinNode && h.MinNode > required {
		at := ""
		if h.Known {
			at = fmt.Sprintf(" at %04d", h.Applied)
		}
		return fmt.Sprintf("paused: home database%s needs this machine at %04d or newer (it has %04d) — update this machine",
			at, h.MinNode, required)
	}
	return ""
}

// Guard keeps the node's current verdict, re-checked in the background.
type Guard struct {
	DB       *sql.DB
	Required int // 0: SchemaRev
	Logger   *slog.Logger
	// ReadFn replaces Read (tests).
	ReadFn func(ctx context.Context) HomeSchema

	mu     sync.RWMutex
	reason string
	home   HomeSchema
	at     time.Time
}

func (g *Guard) required() int {
	if g.Required > 0 {
		return g.Required
	}
	return SchemaRev
}

// Check asks once and returns the reason the node is paused for, or "".
func (g *Guard) Check(ctx context.Context) string {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var h HomeSchema
	if g.ReadFn != nil {
		h = g.ReadFn(qctx)
	} else if g.DB != nil {
		h = Read(qctx, g.DB)
	}
	reason := PauseReason(h, g.required())
	g.mu.Lock()
	prev := g.reason
	g.reason, g.home, g.at = reason, h, time.Now()
	g.mu.Unlock()
	if reason != prev && g.Logger != nil {
		if reason != "" {
			g.Logger.Warn("node: this machine's code does not fit the home's database; its local routes pause",
				"reason", reason, "home_schema", h.Applied, "required", g.required(), "event", "node_paused_schema")
		} else {
			g.Logger.Info("node: the home's database fits this machine",
				"home_schema", h.Applied, "required", g.required(), "event", "node_resumed_schema")
		}
	}
	return reason
}

// Paused is the last verdict ("" = fits).
func (g *Guard) Paused() string {
	if g == nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.reason
}

// Home is the last schema read and when.
func (g *Guard) Home() (HomeSchema, time.Time) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.home, g.at
}

// Run checks at once, then every 5 minutes (every 30 s while paused), until ctx ends.
func (g *Guard) Run(ctx context.Context) {
	for {
		every := CheckEvery
		if g.Check(ctx) != "" {
			every = CheckEveryPaused
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
