package main

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.04: where the home reads its desired state, and the mDNS loop's flags.
func TestComponentsTarget(t *testing.T) {
	cfg := config.Defaults()
	cfg.Database.URL = "postgres://maktaba@127.0.0.1:54329/maktaba"
	if m, dsn, ok := componentsTarget(cfg); !ok || m != store.Home || dsn != cfg.Database.URL {
		t.Fatalf("%q %q %v", m, dsn, ok)
	}
	cfg.Database.URL = "sqlite:///tmp/maktaba.db"
	if _, _, ok := componentsTarget(cfg); ok {
		t.Fatal("a SQLite install reconciles")
	}
}

func TestMDNSLoopFlags(t *testing.T) {
	cfg := config.Defaults()
	noop := func(context.Context) {}
	if l := mdnsLoop(cfg, false, noop); !l.ConfigOn || l.Forced != "" {
		t.Fatalf("%+v", l)
	}
	if l := mdnsLoop(cfg, true, noop); l.Forced != supervisor.ReasonBootFlag || l.ForcedOn {
		t.Fatalf("--no-mdns: %+v", l)
	}
	t.Setenv("MAKTABA_DISCOVERY_MDNS", "0")
	if l := mdnsLoop(cfg, false, noop); l.Forced != supervisor.ReasonEnvOverride || l.ForcedOn {
		t.Fatalf("env: %+v", l)
	}
	cfg.Server.MDNS = "off"
	t.Setenv("MAKTABA_DISCOVERY_MDNS", "")
	if l := mdnsLoop(cfg, false, noop); l.ConfigOn {
		t.Fatalf("[server] mdns = off: %+v", l)
	}
}

// With no reconciler (a single role, SQLite) the loops run as before: on unless off.
func TestStartComponentsWithoutReconciler(t *testing.T) {
	cfg := config.Defaults()
	ran := make(chan string, 2)
	loops := []*supervisor.Loop{
		{Name: "supervisor:backups", ConfigOn: true, Run: func(context.Context) { ran <- "backups" }},
		{Name: "supervisor:mdns", ConfigOn: true, Forced: supervisor.ReasonBootFlag,
			Run: func(context.Context) { ran <- "mdns" }},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startComponents(ctx, cfg, nil, false, loops, "", nil)
	select {
	case got := <-ran:
		if got != "backups" {
			t.Fatalf("%s ran", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backups did not run")
	}
	select {
	case got := <-ran:
		t.Fatalf("%s ran", got)
	case <-time.After(50 * time.Millisecond):
	}
}
