package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// #640: serve --offline (or MAKTABA_OFFLINE=1) turns every outbound switch off for the
// server and its roles; without it nothing changes.
func TestApplyOffline(t *testing.T) {
	for k := range offlineEnv {
		t.Setenv(k, "")
	}
	cfg := config.Config{}
	cfg.Cloud.Enabled = true
	cfg.Components.Packs, cfg.LLM.Pull, cfg.Telemetry.Enabled = "on", "auto", "on"
	if applyOffline(&cfg, false, nil) || !cfg.Cloud.Enabled || cfg.Components.Packs != "on" {
		t.Fatalf("not offline, and yet changed: %+v", cfg)
	}

	var log bytes.Buffer
	if !applyOffline(&cfg, true, &log) {
		t.Fatal("--offline: not offline")
	}
	if cfg.Cloud.Enabled || cfg.Components.PacksOn() || cfg.LLM.Pull != "off" || cfg.Telemetry.Enabled != "off" {
		t.Fatalf("offline: cloud %v, packs %q, pull %q, telemetry %q",
			cfg.Cloud.Enabled, cfg.Components.Packs, cfg.LLM.Pull, cfg.Telemetry.Enabled)
	}
	for k, v := range offlineEnv {
		if os.Getenv(k) != v {
			t.Errorf("%s = %q, want %q", k, os.Getenv(k), v)
		}
	}
	if !ports.Offline() || !strings.Contains(log.String(), "offline") {
		t.Fatalf("log %q", log.String())
	}

	// the environment alone does it too (a test stack sets it for serve)
	t.Setenv(ports.EnvOffline, "1")
	cfg2 := config.Config{}
	cfg2.Cloud.Enabled = true
	if !applyOffline(&cfg2, false, nil) || cfg2.Cloud.Enabled {
		t.Fatal("MAKTABA_OFFLINE=1 alone: still online")
	}
}
