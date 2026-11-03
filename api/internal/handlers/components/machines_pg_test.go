package components

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.12 on Postgres: a machine's override as the machine reads it (T06), its
// stage switches through the real foreign keys, the guard on its status (AC 6) and a
// silent machine's last status (T12).

type pgNode struct {
	id string
	db *sql.DB
}

// pgJoin is an approved machine with its own login, a member of maktaba_worker.
func pgJoin(t *testing.T, db *sql.DB, dsn, name string) pgNode {
	t.Helper()
	id := uuid.NewString()
	role := cluster.RoleName(id)
	roles := &cluster.PGRoles{DB: db}
	if err := roles.Create(context.Background(), role, "node-pass-"+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP OWNED BY ` + pq.QuoteIdentifier(role))
		_ = roles.Drop(context.Background(), role)
	})
	if _, err := db.Exec(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by,
		capabilities, last_seen_at) VALUES ($1, $2, 'approved', 'linux', 'amd64', $3, $4, now(), 'test',
		'{"os":"linux","arch":"x86_64","memory_gb":32}', now())`, id, name, "k-"+name, role); err != nil {
		t.Fatal(err)
	}
	ndb, err := sql.Open("postgres", clustertest.WithUser(t, dsn, role, "node-pass-"+name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ndb.Close() })
	return pgNode{id: id, db: ndb}
}

func pgMachineFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f, dsn := pgFixture(t)
	for _, st := range []string{"extract", "transcribe"} {
		if _, err := f.db.Exec(`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane)
			VALUES ($1, $1, '{video}', 'cpu', 'media') ON CONFLICT DO NOTHING`, st); err != nil {
			t.Fatal(err)
		}
	}
	changed := []string{}
	s := &svc.Service{Store: f.st, Catalog: components.Default(), OnMachineChanged: func(n string) { changed = append(changed, n) }}
	r := chi.NewRouter()
	(&Handler{Service: s, DB: f.db}).Mount(r)
	f.r = r
	return f, dsn
}

// T06 + AC 1: "Off on this machine" for N: the override is installed, extract and
// transcribe are switched off for N, and N's own login reads installed through
// component_desired_for while the home keeps it on.
func TestPGMachineOverride(t *testing.T) {
	f, dsn := pgMachineFixture(t)
	n := pgJoin(t, f.db, dsn, "n")
	decode(t, f.do("PUT", "/api/components/transcription/machines/"+n.id, `{"want":"installed"}`, admin()), 200, nil)
	got, _, err := (&store.Store{DB: n.db}).Desired(context.Background(), n.id)
	if err != nil {
		t.Fatal(err)
	}
	if got["transcription"].Want != components.WantInstalled {
		t.Fatalf("the machine reads %+v", got["transcription"])
	}
	home, _, _ := f.st.Desired(context.Background(), store.Home)
	if home["transcription"].Want != components.WantEnabled {
		t.Fatal("the home's row moved")
	}
	rows, err := f.db.Query(`SELECT stage, enabled FROM cluster_node_stages WHERE node_id = $1 ORDER BY stage`, n.id)
	if err != nil {
		t.Fatal(err)
	}
	var off []string
	for rows.Next() {
		var st string
		var on bool
		_ = rows.Scan(&st, &on)
		if !on {
			off = append(off, st)
		}
	}
	_ = rows.Close()
	if len(off) != 2 || off[0] != "extract" || off[1] != "transcribe" {
		t.Fatalf("switched off %v", off)
	}
	// the machine's own stage switches read the same (what a release before Epic 51 obeys)
	var n2 int
	if err := n.db.QueryRow(`SELECT count(*) FROM cluster_node_stage_switches($1::uuid) WHERE NOT enabled`, n.id).Scan(&n2); err != nil || n2 != 2 {
		t.Fatalf("the machine's switches: %d %v", n2, err)
	}
	// follow the home: the machine reads enabled again, the switches go
	decode(t, f.do("PUT", "/api/components/transcription/machines/"+n.id, `{"want":null}`, admin()), 200, nil)
	got, _, _ = (&store.Store{DB: n.db}).Desired(context.Background(), n.id)
	if got["transcription"].Want != components.WantEnabled {
		t.Fatalf("after follow %+v", got["transcription"])
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM cluster_node_stages WHERE node_id = $1`, n.id).Scan(&n2); err != nil || n2 != 0 {
		t.Fatalf("switches left %d %v", n2, err)
	}
	// a machine removed takes its overrides and switches with it
	decode(t, f.do("PUT", "/api/components/transcription/machines/"+n.id, `{"want":"installed"}`, admin()), 200, nil)
	if _, err := f.db.Exec(`DELETE FROM cluster_nodes WHERE id = $1`, n.id); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM component_overrides WHERE node_id = $1) +
		(SELECT count(*) FROM cluster_node_stages WHERE node_id = $1)`, n.id).Scan(&n2); err != nil || n2 != 0 {
		t.Fatalf("left after delete %d %v", n2, err)
	}
}

// AC 6: N's credentials cannot report M's status; N's own report shows on the
// machine's list. T12: silent past 90 s, the last status shows, stale.
func TestPGMachineStatusGuardAndStale(t *testing.T) {
	f, dsn := pgMachineFixture(t)
	n := pgJoin(t, f.db, dsn, "n")
	m := pgJoin(t, f.db, dsn, "m")
	ns := &store.Store{DB: n.db}
	row := []store.Status{{ID: "transcription", Phase: components.PhaseRunning}}
	if err := ns.ReportStatus(context.Background(), m.id, "pipeline", row); err == nil {
		t.Fatal("N reported M's status")
	}
	if err := ns.ReportStatus(context.Background(), n.id, "pipeline", row); err != nil {
		t.Fatal(err)
	}
	var l svc.MachineList
	decode(t, f.do("GET", "/api/components?machine="+n.id, "", admin()), 200, &l)
	if l.Machine.Stale || l.Machine.Platform != "linux-amd64" {
		t.Fatalf("fresh machine %+v", l.Machine)
	}
	if _, err := f.db.Exec(`UPDATE cluster_nodes SET last_seen_at = now() - interval '2 minutes' WHERE id = $1`, n.id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE component_status SET updated_at = now() - interval '2 minutes' WHERE machine = $1`, n.id); err != nil {
		t.Fatal(err)
	}
	decode(t, f.do("GET", "/api/components?machine="+n.id, "", admin()), 200, &l)
	for _, c := range l.Components {
		if c.ID == "transcription" && (!c.Here.Stale || !c.Here.Reported || c.Here.Phase != components.PhaseRunning ||
			c.Here.UpdatedAt == nil || time.Since(*c.Here.UpdatedAt) < 90*time.Second) {
			t.Fatalf("silent machine %+v", c.Here)
		}
	}
	if !l.Machine.Stale {
		t.Fatalf("machine %+v", l.Machine)
	}
}
