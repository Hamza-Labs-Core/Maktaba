package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/pressly/goose/v3"
)

// A migration that failed part way is undone by its own Down script (the rule since #831:
// a failed update is undone by the migrations' Down scripts, the dump only when they fail).
// goose rolls a migration in a transaction back by itself, so only a NO TRANSACTION one can
// leave work behind: the statements that ran before the one that failed (on 2026-11-23 and
// 2026-11-24, 0235_search_terms.sql's table and first index, then a CREATE INDEX
// CONCURRENTLY cancelled by its lock timeout). goose did not record it, so `down-to` never
// runs its Down; `undo-failed` does, then drops the INVALID indexes the cancelled builds
// left. Every Down script runs after a partial Up, or none at all (migration-lint's
// down-script rule and the down-after-partial rehearsal: api/migrate_down_e2e_test.go), so
// this is harmless when the migration did not start.

// noTransactionMigration is goose's NO TRANSACTION directive.
var noTransactionMigration = regexp.MustCompile(`(?m)^\s*--\s*\+goose\s+NO\s+TRANSACTION\b`)

// runUndoFailed runs the Down script of the first migration of dir goose has not recorded
// on db, when it runs outside a transaction (what it did part way stays), and drops the
// INVALID indexes its cancelled builds left. Nothing is recorded: the database is then as
// it was before that migration started.
func runUndoFailed(ctx context.Context, db *sql.DB, dir string) error {
	current, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("the schema's version: %w", err)
	}
	stage, err := stagePostgresMigrations(dir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	ms, err := goose.CollectMigrations(stage, current, goose.MaxVersion)
	if err != nil {
		return err
	}
	var next *goose.Migration
	for _, m := range ms {
		if m.Version > current && (next == nil || m.Version < next.Version) {
			next = m
		}
	}
	if next == nil {
		fmt.Fprintf(os.Stderr, "migrate: no migration above %d: nothing failed part way\n", current)
		return repairInvalidIndexes(ctx, db, stage)
	}
	raw, err := os.ReadFile(next.Source)
	if err != nil {
		return err
	}
	name := filepath.Base(next.Source)
	if !noTransactionMigration.Match(raw) {
		fmt.Fprintf(os.Stderr, "migrate: %s runs in a transaction: a failure left nothing of it\n", name)
		return repairInvalidIndexes(ctx, db, stage)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(stage), goose.WithDisableVersioning(true))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "migrate: undoing what %s did before it failed, with its Down script (it is not "+
		"recorded: %d is)\n", name, current)
	if _, err := p.ApplyVersion(ctx, next.Version, false); err != nil {
		return fmt.Errorf("the Down script of %s, which failed part way: %w", name, err)
	}
	// the Down script dropped what it made; a build cut short on a table it did not make
	// (an index on videos, say) is left INVALID: dropped here
	return repairInvalidIndexes(ctx, db, stage)
}
