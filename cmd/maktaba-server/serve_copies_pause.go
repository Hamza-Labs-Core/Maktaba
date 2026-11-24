package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The machines' copies during an update's migrations (pgbundle/copies_pause.go): the
// database step waits for their table syncs to end, then pauses them (their replication
// roles may not log in, their walsenders ended) until the migrations are done. Research
// item 5 (E4, E8): on 2026-11-23 the migrations ended 25 to 28 sessions a try, and the
// machines reconnected at once; a table sync's walsender ended mid-COPY makes its machine
// copy that table again, and its snapshot was what the index builds waited on.

// The bundled Postgres's side (tests replace them).
var (
	tableSyncsBundled    = func(ctx context.Context, b pgbundle.Bundle) (int, error) { return b.TableSyncs(ctx) }
	replicaLoginsBundled = func(ctx context.Context, b pgbundle.Bundle) ([]string, error) { return b.ReplicaLogins(ctx) }
	pauseCopiesBundled   = func(ctx context.Context, b pgbundle.Bundle, roles []string) (int, error) {
		return b.PauseCopies(ctx, roles)
	}
	resumeCopiesBundled = func(ctx context.Context, b pgbundle.Bundle, roles []string) error {
		return b.ResumeCopies(ctx, roles)
	}
)

// copySyncWait is how long the migrations wait for the machines' table syncs to end
// before they pause the copies all the same (that machine copies its table again), and
// copySyncEvery how often they look.
var (
	copySyncWait  = 10 * time.Minute
	copySyncEvery = 5 * time.Second
)

// copiesPausedPath is where the paused roles are kept until they log in again (a server
// stopped during the migrations lets them back in at its next start).
func copiesPausedPath(stateDir string) string {
	return filepath.Join(stateDir, "state", "copies-paused.json")
}

// pauseCopies waits (up to wait) for the machines' table syncs to end, then
// pauses their copies for the migrations; the returned func lets them log in again. Only
// the bundled Postgres's copies (an operator's keeps none unless allowed, and its roles
// are the operator's to change). Best effort: what fails is said, and the migrations go
// on as before (their lock timeout, and freeMigrations, bound what they wait on).
func (g *migrationGate) pauseCopies(ctx context.Context, wait time.Duration) func() {
	if g.pg == nil {
		return func() {}
	}
	b := *g.pg
	start, told := time.Now(), false
	for {
		n, err := tableSyncsBundled(ctx, b)
		if err != nil {
			fmt.Fprintf(g.log, "[serve] level=warn the machines' table syncs cannot be counted before the migrations: %v\n", err)
			break
		}
		if n == 0 {
			if told {
				fmt.Fprintf(g.log, "[serve] the machines' table syncs ended (%s): the migrations go on\n",
					time.Since(start).Round(time.Second))
			}
			break
		}
		if time.Since(start) >= wait {
			fmt.Fprintf(g.log, "[serve] level=warn %d of the machines' table syncs still run (%s waited): the copies are "+
				"paused all the same, and those machines copy those tables again\n", n, wait)
			break
		}
		if !told {
			told = true
			fmt.Fprintf(g.log, "[serve] %d of the machines' table syncs run (a machine copying the database): the "+
				"migrations wait for them to end (at most %s)\n", n, wait)
		}
		select {
		case <-ctx.Done():
			return func() {}
		case <-time.After(copySyncEvery):
		}
	}
	roles, err := replicaLoginsBundled(ctx, b)
	if err != nil {
		fmt.Fprintf(g.log, "[serve] level=warn the machines' copies cannot be paused for the migrations: %v\n", err)
		return func() {}
	}
	if len(roles) == 0 {
		return func() {}
	}
	// kept first: a stop during the migrations lets them back in at the next start
	if err := writeCopiesPaused(g.stateDir, roles); err != nil {
		fmt.Fprintf(g.log, "[serve] level=warn the machines' copies are not paused (their roles cannot be kept in %s): %v\n",
			copiesPausedPath(g.stateDir), err)
		return func() {}
	}
	ended, err := pauseCopiesBundled(ctx, b, roles)
	if err != nil {
		fmt.Fprintf(g.log, "[serve] level=warn the machines' copies could not all be paused for the migrations: %v\n", err)
	} else {
		fmt.Fprintf(g.log, "[serve] the machines' copies are paused for the migrations (%s may not log in; %d of "+
			"their sessions ended): they go on from where they stopped once the migrations are done\n",
			strings.Join(roles, ", "), ended)
	}
	return func() { g.resumeCopies(roles) }
}

// resumeCopies lets the paused roles log in again (a minute at most: the migrations are
// over, or the server stops).
func (g *migrationGate) resumeCopies(roles []string) {
	if g.pg == nil || len(roles) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := resumeCopiesBundled(ctx, *g.pg, roles); err != nil {
		fmt.Fprintf(g.log, "[serve] level=error the machines' copies could not log in again after the migrations "+
			"(%s): %v; the next start tries again\n", strings.Join(roles, ", "), err)
		return
	}
	if err := os.Remove(copiesPausedPath(g.stateDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(g.log, "[serve] level=warn %s cannot be removed: %v\n", copiesPausedPath(g.stateDir), err)
	}
	fmt.Fprintf(g.log, "[serve] the machines' copies may log in again (%s)\n", strings.Join(roles, ", "))
}

// resumeLeftCopies lets back in the copies a step that stopped during its migrations left
// paused (copiesPausedPath).
func (g *migrationGate) resumeLeftCopies() {
	roles, err := readCopiesPaused(g.stateDir)
	if err != nil {
		fmt.Fprintf(g.log, "[serve] level=warn %s cannot be read: %v\n", copiesPausedPath(g.stateDir), err)
		return
	}
	if len(roles) > 0 {
		fmt.Fprintf(g.log, "[serve] the machines' copies were left paused by migrations that did not finish: they may "+
			"log in again\n")
		g.resumeCopies(roles)
	}
}

func writeCopiesPaused(stateDir string, roles []string) error {
	b, _ := json.Marshal(map[string]any{"roles": roles, "at": time.Now().UTC()})
	if err := os.MkdirAll(filepath.Dir(copiesPausedPath(stateDir)), 0o755); err != nil {
		return err
	}
	return layout.WriteFileAtomic(copiesPausedPath(stateDir), append(b, '\n'), 0o600)
}

func readCopiesPaused(stateDir string) ([]string, error) {
	b, err := os.ReadFile(copiesPausedPath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v.Roles, nil
}
