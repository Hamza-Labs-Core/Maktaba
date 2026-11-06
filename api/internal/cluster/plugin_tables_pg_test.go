package cluster

import (
	"context"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Epic 54 (slot 0191): a joined machine (maktaba_worker) keeps the default grants on the
// plugin tables (the previous release's worker-group check reads a revoke as a change)
// and may read them, but cannot write a row: only the home's API writes them.
func TestPluginTablesAreTheHomes(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest)
		VALUES ('dev.example.x', '1.0.0', 'd', '/p', '{}')`); err != nil {
		t.Fatalf("the home writes: %v", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET ROLE `+WorkerGroup); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), `RESET ROLE`) }()
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM plugin_installs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a machine reads the installs: %d %v", n, err)
	}
	for _, q := range []string{
		`INSERT INTO plugin_grants (plugin_id) VALUES ('dev.example.x')`,
		`INSERT INTO plugin_data (plugin_id, subject_kind, subject_id, key, value, bytes) VALUES ('a','video','v','k','1',1)`,
		`INSERT INTO plugin_suggestions (plugin_id, version, subject_kind, subject_id, field, value) VALUES ('a','1','video','v','f','x')`,
		`UPDATE plugin_installs SET version = '2.0.0'`,
		`DELETE FROM plugin_installs`,
	} {
		if _, err := conn.ExecContext(ctx, q); err == nil || !strings.Contains(err.Error(), "only the home writes plugin tables") {
			t.Errorf("%s as a joined machine: %v", q, err)
		}
	}
}
