package cluster

// The schema guard of a streaming node (Epic 31 design §5.1, the same rule as the
// pipeline's cluster/schema.py): the node's code is written against the migrations it
// ships (SchemaRev); the home migrates its own database, a node never does. A home whose
// applied version is below SchemaRev, or whose maktaba_min_node_schema() names a newer
// schema than SchemaRev (a migration that breaks older code declares it), pauses the
// node: it streams nothing until the home (or this machine) is updated, then starts by
// itself. A home whose version cannot be read is not judged.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// SchemaRev is the highest migration slot this code ships (shared/db/migrations); a
// test fails when a migration is added without raising it.
const SchemaRev = 137

// MinNodeFunction is what a migration that breaks older nodes (re)defines: RETURNS
// integer, the oldest node schema it still works with.
const MinNodeFunction = "maktaba_min_node_schema"

// SchemaCheckInterval is how often a paused node asks again.
const SchemaCheckInterval = 30 * time.Second

// HomeSchema is what the home's database says about its schema.
type HomeSchema struct {
	Applied    int  // max(goose_db_version.version_id) where is_applied
	Known      bool // Applied could be read
	MinNode    int  // maktaba_min_node_schema()
	HasMinNode bool // the home declares a breaking change
}

// RowQuerier is the part of a pgx pool the guard uses.
type RowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ReadHomeSchema reads the applied version and the breaking declaration (the
// machines' role reads goose's table: slot 0103 granted every table of the schema).
func ReadHomeSchema(ctx context.Context, q RowQuerier) HomeSchema {
	var h HomeSchema
	var applied int64
	if err := q.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&applied); err == nil {
		h.Applied, h.Known = int(applied), true
	}
	var has bool
	if err := q.QueryRow(ctx, `SELECT to_regproc('`+MinNodeFunction+`') IS NOT NULL`).Scan(&has); err == nil && has {
		var n int64
		if err := q.QueryRow(ctx, `SELECT `+MinNodeFunction+`()`).Scan(&n); err == nil {
			h.MinNode, h.HasMinNode = int(n), true
		}
	}
	return h
}

// PauseReason is "paused: <why>" when a node with schema `required` must not run
// against h, else "". The words match the pipeline's (the Machines page reads both).
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

// SchemaGuard checks the home's schema for one node process and logs a pause and a
// resume once each.
type SchemaGuard struct {
	Q        RowQuerier
	Required int // 0: SchemaRev
	Interval time.Duration
	Logger   *slog.Logger
	// Read replaces ReadHomeSchema (tests)
	Read func(ctx context.Context) HomeSchema

	paused string
}

func (g *SchemaGuard) required() int {
	if g.Required > 0 {
		return g.Required
	}
	return SchemaRev
}

// Check asks once; the reason the node is paused for, or "".
func (g *SchemaGuard) Check(ctx context.Context) string {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var h HomeSchema
	if g.Read != nil {
		h = g.Read(qctx)
	} else {
		h = ReadHomeSchema(qctx, g.Q)
	}
	reason := PauseReason(h, g.required())
	if reason != g.paused && g.Logger != nil {
		if reason != "" {
			g.Logger.Warn("tv: this node's code needs a newer home database; it streams nothing",
				"reason", reason, "home_schema", h.Applied, "required", g.required(), "event", "tv_node_paused_schema")
		} else {
			g.Logger.Info("tv: the home's database fits this node again",
				"home_schema", h.Applied, "required", g.required(), "event", "tv_node_resumed_schema")
		}
	}
	g.paused = reason
	return reason
}

// Wait returns once the schema fits (nil) or ctx ends (its error).
func (g *SchemaGuard) Wait(ctx context.Context) error {
	every := g.Interval
	if every <= 0 {
		every = SchemaCheckInterval
	}
	for g.Check(ctx) != "" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
	return nil
}
