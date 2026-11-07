package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// Epic 50 story 50.15: direct access is built only on the home that runs the relay agent (the
// supervisor passes MAKTABA_DIRECT_*), and reads rules.direct, off by default and when offline.
func TestDirectServiceNeedsTheAgent(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("MAKTABA_DIRECT_DIR", "")
	if directService(nil, "127.0.0.1:8765", log) != nil {
		t.Fatal("built without MAKTABA_DIRECT_*")
	}
	t.Setenv("MAKTABA_DIRECT_DIR", t.TempDir())
	t.Setenv("MAKTABA_DIRECT_LISTEN", ":8444")
	t.Setenv("MAKTABA_DIRECT_UPSTREAM", "http://127.0.0.1:8088")
	if directService(nil, "", log) != nil {
		t.Fatal("built without the agent")
	}
	s := directService(nil, "127.0.0.1:8765", log)
	if s == nil || s.Listen != ":8444" || s.Upstream.Host != "127.0.0.1:8088" || s.Agent.Addr != "127.0.0.1:8765" {
		t.Fatalf("service %+v", s)
	}
	st, err := directSettings(context.Background(), nil)
	if err != nil || st.Enabled || !st.MapPort || st.ManualPort != 0 {
		t.Fatalf("defaults: %+v %v", st, err)
	}
	t.Setenv("MAKTABA_OFFLINE", "1")
	if st, _ := directSettings(context.Background(), nil); st.Enabled || st.MapPort {
		t.Fatalf("offline: %+v", st)
	}
}
