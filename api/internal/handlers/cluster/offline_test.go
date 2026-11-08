package cluster

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// Story 53.12: the dashboards' Overview ranks the joined machines that are offline first;
// which those are follows the Machines page's own rule: approved, and neither the machine
// nor any worker of it seen within OnlineWindow. Pending and revoked machines are not
// joined, so never offline.
func TestOfflineMachines(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	store := cluster.NewMemoryStore()
	for _, id := range []string{"n-up", "n-stale", "n-silent", "n-pending", "n-revoked"} {
		if err := store.PutPending(ctx, cluster.Node{ID: id, Name: id, CreatedAt: now.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if id == "n-pending" {
			continue
		}
		if err := store.Approve(ctx, id, "r_"+id, nil, "admin", now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Revoke(ctx, "n-revoked", now); err != nil {
		t.Fatal(err)
	}
	store.AddWorker(cluster.NodeWorker{ID: "w1", NodeID: "n-up", LastSeenAt: now.Add(-10 * time.Second)})
	store.AddWorker(cluster.NodeWorker{ID: "w2", NodeID: "n-stale", LastSeenAt: now.Add(-5 * time.Minute)})
	h := &Handler{Store: store, NowFunc: func() time.Time { return now }}
	got, err := h.OfflineMachines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "n-silent,n-stale" {
		t.Fatalf("offline %v, want n-silent and n-stale", got)
	}
	// all online: an empty list, not nil
	store.AddWorker(cluster.NodeWorker{ID: "w3", NodeID: "n-stale", LastSeenAt: now})
	store.AddWorker(cluster.NodeWorker{ID: "w4", NodeID: "n-silent", LastSeenAt: now})
	if got, err := h.OfflineMachines(ctx); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("all online: %v %v", got, err)
	}
}

// Story 53.12: the Overview names each machine as the Machines page does: a joined machine
// by its name, the home (machine id "home") by the home's own name. Only the ids asked are
// answered; an id with no name is left out (the dashboards fall back to the id).
func TestMachineNames(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	store := cluster.NewMemoryStore()
	if _, err := store.EnsureIdentity(ctx, "Living room"); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[string]string{"n-1": "Attic NAS", "n-2": "Office", "n-3": ""} {
		if err := store.PutPending(ctx, cluster.Node{ID: id, Name: name, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{Store: store, NowFunc: func() time.Time { return now }}
	got, err := h.MachineNames(ctx, []string{"home", "n-1", "n-3", "n-gone"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"home": "Living room", "n-1": "Attic NAS"}
	if len(got) != len(want) {
		t.Fatalf("names %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("names %v, want %v", got, want)
		}
	}
	if got, err := h.MachineNames(ctx, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no ids: %v %v", got, err)
	}
}
