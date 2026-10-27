package clusterauth

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// A copy still being made says nothing of when it last heard the home: its beat may be in
// before the people are, and a sign-in checked on it would be refused as a wrong password
// (a node signed in right after it started, TestTwoMachinesOnePC).
func TestACopyStillBeingMadeIsNotSynced(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	beat := time.Now().UTC().Truncate(time.Second)
	if _, err := db.ExecContext(ctx, `INSERT INTO cluster_replica_beat (id, at) VALUES (1, $1)
	  ON CONFLICT (id) DO UPDATE SET at = EXCLUDED.at`, beat); err != nil {
		t.Fatal(err)
	}
	c := NewCopy(db, nodeA)
	// no subscription here: the catalog answers, nothing is being copied
	at, err := c.Synced(ctx)
	if err != nil || !at.Equal(beat) {
		t.Fatalf("a copy with every table copied: %v %v, want %v", at, err, beat)
	}
	c.Copying = func(context.Context) (bool, error) { return true, nil }
	if at, err := c.Synced(ctx); err != nil || !at.IsZero() {
		t.Fatalf("a copy still being made: %v %v, want zero", at, err)
	}
}
