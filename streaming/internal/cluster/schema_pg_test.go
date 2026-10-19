package cluster_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// A machine's login reads the schema of a home migrated by goose, and the breaking
// declaration once a migration makes one.
func TestReadHomeSchemaAsAMachineRole(t *testing.T) {
	dsn, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	var hasGroup bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')`).Scan(&hasGroup)
	if !hasGroup {
		t.Skip("the migrating user cannot create roles (no maktaba_worker)")
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	role := "e31s_" + suffix
	if _, err := pool.Exec(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD 'pw-`+suffix+`' IN ROLE maktaba_worker`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, role)
		_, _ = pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+role)
	})
	node, err := pgxpool.New(ctx, pgtest.WithUser(t, dsn, role, "pw-"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.Ping(ctx); err != nil {
		t.Skipf("cannot log in as a machine role here: %v", err)
	}

	h := cluster.ReadHomeSchema(ctx, node)
	if !h.Known || h.Applied != cluster.SchemaRev || h.HasMinNode {
		t.Fatalf("ReadHomeSchema = %+v, want applied %d, no breaking declaration", h, cluster.SchemaRev)
	}
	if r := cluster.PauseReason(h, cluster.SchemaRev); r != "" {
		t.Fatalf("a fitting home pauses: %q", r)
	}
	if r := cluster.PauseReason(h, cluster.SchemaRev+5); !strings.HasPrefix(r, "paused: home database at ") {
		t.Fatalf("newer code against this home: %q", r)
	}

	if _, err := pool.Exec(ctx, `CREATE FUNCTION `+cluster.MinNodeFunction+`() RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT 9999'`); err != nil {
		t.Fatal(err)
	}
	h = cluster.ReadHomeSchema(ctx, node)
	if !h.HasMinNode || h.MinNode != 9999 {
		t.Fatalf("breaking declaration not read: %+v", h)
	}
	if r := cluster.PauseReason(h, cluster.SchemaRev); !strings.Contains(r, "update this machine") {
		t.Fatalf("a breaking home does not pause older code: %q", r)
	}
}
