package components

import (
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.14: a machine's health comes from its parts' rows and their detail.
func TestHealthOf(t *testing.T) {
	cat := components.Default()
	llm, _ := cat.Get("llm")
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	rows := []store.Status{
		{Machine: "home", ID: "llm", Part: "supervisor", Phase: "running", Version: "1.2.3", Since: t0.Add(time.Minute),
			UpdatedAt: t0.Add(2 * time.Minute),
			Detail: map[string]any{"restarts": float64(3), "rss_mb": float64(512),
				"last_error": map[string]any{"code": "crash_loop", "at": t0.Add(-time.Hour).Format(time.RFC3339Nano)}}},
		{Machine: "home", ID: "llm", Part: "api", Phase: "running", Since: t0, UpdatedAt: t0},
	}
	h := healthOf(llm, rows, "running", 0, false)
	if h == nil || h.Since == nil || !h.Since.Equal(t0.Add(time.Minute)) {
		t.Fatalf("since = %+v", h)
	}
	if h.Restarts != 3 || h.Version != "1.2.3" || h.RSSMB == nil || *h.RSSMB != 512 || h.DiskMB != nil {
		t.Fatalf("health = %+v", h)
	}
	if h.LastErrorCode != "crash_loop" || h.LastErrorAt == nil || !h.LastErrorAt.Equal(t0.Add(-time.Hour)) {
		t.Fatalf("last error = %q %v", h.LastErrorCode, h.LastErrorAt)
	}
	if h.UpdatedAt == nil || !h.UpdatedAt.Equal(t0.Add(2*time.Minute)) || h.Stale {
		t.Fatalf("updated / stale = %v %v", h.UpdatedAt, h.Stale)
	}

	// a failed part is the last error, and a failed component has no uptime
	failed := healthOf(llm, []store.Status{{ID: "llm", Part: "supervisor", Phase: "failed", Since: t0,
		Conditions: []store.Condition{{Type: "Ready", Status: "false", Reason: "crash_loop"}}}}, "failed", 0, true)
	if failed.Since != nil || failed.LastErrorCode != "crash_loop" || !failed.LastErrorAt.Equal(t0) || !failed.Stale {
		t.Fatalf("failed = %+v", failed)
	}

	// an installed asset is the catalog's size on disk; one not installed takes none
	model, _ := cat.Get("model.whisper-turbo")
	on := healthOf(model, []store.Status{{ID: model.ID, Part: "supervisor", Phase: "off"}}, "off", 1500, false)
	if on.DiskMB == nil || *on.DiskMB != 1500 {
		t.Fatalf("asset disk = %v", on.DiskMB)
	}
	if out := healthOf(model, []store.Status{{ID: model.ID, Phase: "not_installed"}}, "not_installed", 1500, false); out.DiskMB != nil {
		t.Fatalf("not installed disk = %v", *out.DiskMB)
	}
	if healthOf(llm, nil, "", 0, false) != nil {
		t.Fatal("no rows, no health")
	}
}

func TestSnapshotStale(t *testing.T) {
	now := time.Now()
	snap := &snapshot{heard: map[string]time.Time{"n1": now.Add(-10 * time.Second), "n2": now.Add(-2 * time.Minute)}}
	if snap.stale(store.Home, now) || snap.stale("n1", now) || !snap.stale("n2", now) || !snap.stale("n3", now) {
		t.Fatal("stale marks")
	}
	if (&snapshot{}).stale("n2", now) {
		t.Fatal("unreadable heartbeats mark nothing stale")
	}
}
