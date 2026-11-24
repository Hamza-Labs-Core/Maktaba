package updates

import (
	"context"
	"testing"
	"time"
)

// Research item 5: no automatic run starts while a machine copies a table of the home's
// database (a table sync's slot on the home); a database that cannot tell (SQLite) is not
// held. On Postgres the slot is made as a table sync names it, when the server allows
// logical slots: an active one holds the run; one left by a machine that went away
// (permanent, inactive) never does.
func TestCopyingBlocker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := CopyingBlocker{DB: be.db}
			if why, err := b.Blocked(ctx); err != nil || why != "" {
				t.Fatalf("no copy: %q %v", why, err)
			}
			if be.name == "sqlite" {
				return
			}
			const slot = "pg_16999_sync_16384_7300000000000000002"
			// a slot being made is active: it waits for the transactions under way
			tx, err := be.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`SELECT pg_current_xact_id()`); err != nil {
				t.Fatal(err)
			}
			made := make(chan error, 1)
			go func() {
				_, err := be.db.Exec(`SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, slot)
				made <- err
			}()
			t.Cleanup(func() { _, _ = be.db.Exec(`SELECT pg_drop_replication_slot($1)`, slot) })
			why := ""
			for end := time.Now().Add(20 * time.Second); why == "" && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
				select {
				case err := <-made:
					_ = tx.Rollback()
					t.Skipf("no logical slot on this server: %v", err)
				default:
				}
				if why, err = b.Blocked(ctx); err != nil {
					t.Fatal(err)
				}
			}
			_ = tx.Rollback()
			if why != BlockMachineCopying {
				t.Fatalf("a table sync: %q", why)
			}
			if err := <-made; err != nil {
				t.Fatal(err)
			}
			// the machine gone mid-copy: its slot stays, inactive
			if why, err := b.Blocked(ctx); err != nil || why != "" {
				t.Fatalf("a slot left behind: %q %v", why, err)
			}
		})
	}
	if why, _ := (CopyingBlocker{}).Blocked(ctx); why != "" {
		t.Fatalf("no database: %q", why)
	}
}
