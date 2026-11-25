package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The machines' copies during an update's migrations (pgbundle/copies_pause.go): the
// database step waits for their table syncs to end, then keeps them out (pg_hba.conf, their
// sessions ended) until the schema is settled. Research item 5 (E4, E8): on 2026-11-23 the
// migrations ended 25 to 28 sessions a try, and the machines reconnected at once; a table
// sync's walsender ended mid-COPY makes its machine copy that table again, and its snapshot
// was what the index builds waited on.
//
// The copies come back when the database's record says the schema is settled (schemaSettled):
// the migrations ran, or failed and were kept or undone, or a restore a person asked for is
// done. Not while the record says migrating (the next start goes on), restoring, or held (a
// person has not chosen yet): the marker outlives the server, and each of those starts with
// the copies kept out. A rollback to a release from before the marker writes pg_hba.conf
// without it, so nothing stays paused there.

// The bundled Postgres's side (tests replace them).
var (
	tableSyncsBundled   = func(ctx context.Context, b pgbundle.Bundle) (int, error) { return b.TableSyncs(ctx) }
	pauseCopiesBundled  = func(ctx context.Context, b pgbundle.Bundle) (int, error) { return b.PauseCopies(ctx) }
	resumeCopiesBundled = func(ctx context.Context, b pgbundle.Bundle) error { return b.ResumeCopies(ctx) }
	copiesPausedBundled = func(b pgbundle.Bundle) bool { return b.CopiesPaused() }
)

// copySyncWait is how long the migrations wait for the machines' table syncs to end
// before they pause the copies all the same (that machine copies its table again), and
// copySyncEvery how often they look.
var (
	copySyncWait  = 10 * time.Minute
	copySyncEvery = 5 * time.Second
)

// pauseCopies waits (wait at most, whatever the server does) for the machines' table syncs
// to end, then keeps their copies out. Only the bundled Postgres's copies (an operator's
// keeps none unless allowed, and its pg_hba.conf is the operator's).
//
// When they cannot be kept out, that is said at level=error and the migrations run all the
// same, as on every release before the pause: what they wait on longer than
// migrationBlockerGrace is ended (freeMigrations), their index builds are plain (they wait
// on no snapshot), and the lock timeout bounds the rest. An update that could not start for
// want of the pause would keep a home off its security fixes over a machine's copy, which
// is made anew at worst.
func (g *migrationGate) pauseCopies(ctx context.Context, wait time.Duration) {
	if g.pg == nil {
		return
	}
	b := *g.pg
	if wait > 0 {
		g.waitTableSyncs(ctx, b, wait)
	}
	if ctx.Err() != nil {
		return
	}
	ended, err := pauseCopiesBundled(ctx, b)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(g.log, "[serve] level=error the machines' copies could not be paused: they may come back while "+
				"the schema changes (what the migrations wait on is ended, as before): %v\n", err)
		}
		return
	}
	fmt.Fprintf(g.log, "[serve] the machines' copies are paused until the schema is settled (%d of their sessions "+
		"ended): they go on from where they stopped\n", ended)
}

func (g *migrationGate) waitTableSyncs(ctx context.Context, b pgbundle.Bundle, wait time.Duration) {
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	start, told := time.Now(), 0
	for {
		n, err := tableSyncsBundled(wctx, b)
		switch {
		case ctx.Err() != nil:
			return
		case wctx.Err() != nil:
			fmt.Fprintf(g.log, "[serve] level=warn %d of the machines' table syncs still run (%s waited): the copies are "+
				"paused all the same, and those machines copy those tables again\n", told, wait)
			return
		case err != nil:
			fmt.Fprintf(g.log, "[serve] level=warn the machines' table syncs cannot be counted before the migrations: %v\n", err)
			return
		case n == 0:
			if told > 0 {
				fmt.Fprintf(g.log, "[serve] the machines' table syncs ended (%s): the migrations go on\n",
					time.Since(start).Round(time.Second))
			}
			return
		case told == 0:
			fmt.Fprintf(g.log, "[serve] %d of the machines' table syncs run (a machine copying the database): the "+
				"migrations wait for them to end (at most %s)\n", n, wait)
		}
		told = n
		select {
		case <-wctx.Done():
		case <-time.After(copySyncEvery):
		}
	}
}

// downFailedPath says a rollback's Down script failed on the database: its schema is part way,
// whatever the record says (an update with no backup, [backups] pre_update = "off", cannot be
// held: its record stays migrated).
func downFailedPath(stateDir string) string {
	return filepath.Join(stateDir, "state", "down-failed.json")
}

// noteDownFailed records it for the release the record is of; clearDownFailed forgets it: that
// release's database step ran to its end (its migrations are whole again).
func noteDownFailed(stateDir string) error {
	rec, ok, err := apppayload.ReadDatabase(stateDir)
	if err != nil || !ok {
		return err
	}
	raw, _ := json.Marshal(map[string]any{"version": rec.Version, "at": time.Now().UTC()})
	return layout.WriteFileAtomic(downFailedPath(stateDir), append(raw, '\n'), 0o600)
}

func clearDownFailed(stateDir string) { _ = os.Remove(downFailedPath(stateDir)) }

// downFailed reports whether a Down script failed on the database the record of version
// holds. One that cannot be read says it did.
func downFailed(stateDir, version string) bool {
	raw, err := os.ReadFile(downFailedPath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	var d struct {
		Version string `json:"version"`
	}
	return err != nil || json.Unmarshal(raw, &d) != nil ||
		layout.NormalizeVersion(d.Version) == layout.NormalizeVersion(version)
}

// schemaSettled says whether the database's record holds a schema the machines' copies may
// follow: no migration, restore or held update under way (none recorded: none is), and no
// rollback's Down script failed on it. A
// record that cannot be read, or a state this release does not know, does not.
func schemaSettled(stateDir string) bool {
	rec, ok, err := apppayload.ReadDatabase(stateDir)
	if err != nil {
		return false
	}
	switch rec.State {
	case apppayload.DatabaseMigrated, apppayload.DatabaseRestored:
		return !downFailed(stateDir, rec.Version)
	}
	return !ok
}

// resumeCopies lets the machines' copies in again when they are paused and the schema is
// settled (a minute at most).
func (g *migrationGate) resumeCopies() {
	if g.pg == nil || !copiesPausedBundled(*g.pg) {
		return
	}
	if !schemaSettled(g.stateDir) {
		fmt.Fprintf(g.log, "[serve] the machines' copies stay paused: the library database's schema is not settled yet\n")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := resumeCopiesBundled(ctx, *g.pg); err != nil {
		fmt.Fprintf(g.log, "[serve] level=error the machines' copies could not be let in again (it is tried again): %v\n", err)
		return
	}
	fmt.Fprintf(g.log, "[serve] the machines' copies may log in again\n")
}
