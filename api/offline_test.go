package main

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// #640: MAKTABA_OFFLINE=1 turns the API's own outbound switches off (updates, telemetry);
// without it they are left as they were.
func TestApplyOffline(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for k := range offlineEnv {
		t.Setenv(k, "kept")
	}
	t.Setenv("MAKTABA_OFFLINE", "")
	applyOffline(logger)
	for k := range offlineEnv {
		if os.Getenv(k) != "kept" {
			t.Fatalf("online: %s changed to %q", k, os.Getenv(k))
		}
	}
	t.Setenv("MAKTABA_OFFLINE", "1")
	applyOffline(logger)
	if os.Getenv("MAKTABA_UPDATE_DISABLE") != "1" || os.Getenv("MAKTABA_TELEMETRY_URL") != "off" {
		t.Fatalf("offline: update %q, telemetry %q", os.Getenv("MAKTABA_UPDATE_DISABLE"), os.Getenv("MAKTABA_TELEMETRY_URL"))
	}
	if u := telemetryURL(nil)(); u != "" {
		t.Fatalf("offline: telemetry goes to %q", u)
	}
}
