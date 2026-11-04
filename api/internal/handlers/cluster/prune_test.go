package cluster

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// pruneCounter is the memory store, counting the housekeeping's prunes.
type pruneCounter struct {
	*cluster.MemoryStore
	prunes atomic.Int32
}

func (p *pruneCounter) PruneComponentStatus(context.Context) (int64, error) {
	p.prunes.Add(1)
	return 0, nil
}

// The housekeeping prunes deleted machines' component status rows on every tick (Epic
// 51 story 51.02).
func TestHousekeepingPrunesComponentStatus(t *testing.T) {
	store := &pruneCounter{MemoryStore: cluster.NewMemoryStore()}
	h := &Handler{Store: store, Roles: cluster.Unsupported{Reason: cluster.ReasonSQLite}}
	if _, err := h.Bootstrap(context.Background(), "pi"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx, time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for store.prunes.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the housekeeping never pruned")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}
