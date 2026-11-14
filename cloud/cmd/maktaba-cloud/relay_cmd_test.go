package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

type memAdmin struct{ *relaypkg.MemPoolStore }

func (m memAdmin) SetState(_ context.Context, id, state string) (bool, error) {
	rs, _ := m.Relays(context.Background())
	for _, r := range rs {
		if r.ID == id {
			m.MemPoolStore.SetState(id, state)
			return true, nil
		}
	}
	return false, nil
}

// Story 50.16: list the pool, drain a relay (out of the healthy set), activate it again; an
// unknown relay and a bad command are refused.
func TestRelayCommand(t *testing.T) {
	ctx := context.Background()
	store := memAdmin{relaypkg.NewMemPoolStore()}
	_ = store.Beat(ctx, relaypkg.PoolRelay{ID: "r1", PublicIP: "203.0.113.1", PrivateIP: "10.0.1.1", CapacityMbps: 1000})
	var out bytes.Buffer
	if err := relayCommand(ctx, store, []string{"list"}, &out); err != nil || !strings.Contains(out.String(), "r1") || !strings.Contains(out.String(), "active") {
		t.Fatalf("list: %v %q", err, out.String())
	}
	if err := relayCommand(ctx, store, []string{"drain", "r1"}, &out); err != nil {
		t.Fatal(err)
	}
	if rs, _ := store.Relays(ctx); rs[0].State != relaypkg.RelayDraining || rs[0].Healthy() {
		t.Fatalf("after drain: %+v", rs[0])
	}
	if err := relayCommand(ctx, store, []string{"activate", "r1"}, &out); err != nil {
		t.Fatal(err)
	}
	if rs, _ := store.Relays(ctx); !rs[0].Healthy() {
		t.Fatalf("after activate: %+v", rs[0])
	}
	for _, bad := range [][]string{{"drain", "r9"}, {"drain"}, {"reboot", "r1"}, {}} {
		if err := relayCommand(ctx, store, bad, &out); err == nil {
			t.Errorf("%v: accepted", bad)
		}
	}
}
