package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// Story 51.03: on a home whose roles wait on the database step, every role but Postgres
// and the model server waits on the seed after it; a seed that fails never keeps them
// from starting (it is logged, and the next start tries again). A home with no database
// step (its API migrates) is not held: it seeds in the background.
func TestComponentsSeedStepNeverHoldsTheRoles(t *testing.T) {
	old := componentSeedPause
	componentSeedPause = time.Millisecond
	t.Cleanup(func() { componentSeedPause = old })
	dir := t.TempDir()
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	cfg := config.Config{}
	cfg.Server.DataDir = dir
	// a database with none of the component tables: every try fails
	cfg.Database.URL = "sqlite://" + filepath.Join(dir, "empty.db")
	dbRan := 0
	dbStep := func(context.Context, func(supervisor.Detail)) error { dbRan++; return nil }
	procs := []supervisor.Process{
		{Role: supervisor.RolePostgres},
		{Role: supervisor.RoleAPI, Prepare: dbStep},
		{Role: supervisor.RolePipeline, Prepare: dbStep},
		{Role: supervisor.RoleOllama},
	}
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := componentsAtBoot(ctx, cfg, serveOptions{}, "", nil, nil, procs, &log)
	if got[0].Prepare != nil || got[3].Prepare != nil {
		t.Fatal("Postgres or the model server waits on the seed")
	}
	for _, p := range got[1:3] {
		if err := p.Prepare(ctx, func(supervisor.Detail) {}); err != nil {
			t.Fatalf("%s: %v", p.Role, err)
		}
	}
	if dbRan != 2 {
		t.Fatalf("the database step ran %d times, want once per role", dbRan)
	}
	if n := strings.Count(log.String(), "the seed of desired state failed"); n != 1 {
		t.Fatalf("the failed seed was logged %d times (once for all roles):\n%s", n, log.String())
	}

	// no database step: the roles get no step at all
	plain := []supervisor.Process{{Role: supervisor.RoleAPI}, {Role: supervisor.RolePipeline}}
	for _, p := range componentsAtBoot(ctx, cfg, serveOptions{}, "", nil, nil, plain, &log) {
		if p.Prepare != nil {
			t.Fatalf("%s waits on a seed on a home whose API migrates", p.Role)
		}
	}
}
