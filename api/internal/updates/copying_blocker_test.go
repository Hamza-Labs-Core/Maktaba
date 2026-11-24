package updates

import (
	"context"
	"testing"
)

// Research item 5: no automatic run starts while a machine copies a table of the home's
// database (a table sync's slot on the home); a database that cannot tell (SQLite) is not
// held. On Postgres the slot is made as a table sync names it, when the server allows
// logical slots.
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
			if _, err := be.db.Exec(`SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, slot); err != nil {
				t.Skipf("no logical slot on this server: %v", err)
			}
			t.Cleanup(func() { _, _ = be.db.Exec(`SELECT pg_drop_replication_slot($1)`, slot) })
			if why, err := b.Blocked(ctx); err != nil || why != BlockMachineCopying {
				t.Fatalf("a table sync: %q %v", why, err)
			}
		})
	}
	if why, _ := (CopyingBlocker{}).Blocked(ctx); why != "" {
		t.Fatalf("no database: %q", why)
	}
}
