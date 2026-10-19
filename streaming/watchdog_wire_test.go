package main

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

func TestWatchChannelsRegistersTheEngine(t *testing.T) {
	procWatchdog = newProcWatchdog()
	watchChannels(channel.NewEngine(nil, nil, nil, 1, nil), nil)
	procWatchdog.Beat(context.Background())
	if err := procWatchdog.LiveCheck().Run(context.Background()); err != nil {
		t.Fatalf("live = %v", err)
	}
	if err := procWatchdog.ReadyCheck("channel_engine").Run(context.Background()); err != nil {
		t.Fatalf("engine readiness = %v", err)
	}
}

func TestWatchdogStaleFromEnv(t *testing.T) {
	t.Setenv("MAKTABA_WATCHDOG_STALE", "90s")
	if got := newProcWatchdog().Stale; got != 90*time.Second {
		t.Fatalf("stale = %s", got)
	}
}
