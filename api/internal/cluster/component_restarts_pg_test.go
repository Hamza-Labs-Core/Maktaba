package cluster

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Slot 0186 (#663 review): a joined machine (maktaba_worker) keeps its grants on
// component_restarts but cannot write a row, and no one can move a seq down: both runners
// restart only when the seq moved up, so a lowered one hid every later request.
func TestComponentRestartsAreTheHomesAndOnlyGoUp(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO component_restarts (machine, id, host, seq)
		VALUES ('home', 'queue_health', 'pipeline', 5)`); err != nil {
		t.Fatalf("the home asks: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE component_restarts SET seq = seq + 1`); err != nil {
		t.Fatalf("the home moves it up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE component_restarts SET seq = 1`); err == nil ||
		!strings.Contains(err.Error(), "cannot go down") {
		t.Fatalf("a seq moved down: %v", err)
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
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM component_restarts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a machine reads the requests: %d %v", n, err)
	}
	for _, q := range []string{
		`INSERT INTO component_restarts (machine, id, host, seq) VALUES ('home', 'x', 'streaming', 1)`,
		`UPDATE component_restarts SET seq = seq + 1`,
		`DELETE FROM component_restarts`,
	} {
		if _, err := conn.ExecContext(ctx, q); err == nil || !strings.Contains(err.Error(), "only the home") {
			t.Errorf("%s as a joined machine: %v", q, err)
		}
	}
	var seq int64
	if err := db.QueryRowContext(ctx, `SELECT seq FROM component_restarts`).Scan(&seq); err != nil && err != sql.ErrNoRows || seq != 6 {
		t.Fatalf("seq %d (%v), want 6", seq, err)
	}
}
