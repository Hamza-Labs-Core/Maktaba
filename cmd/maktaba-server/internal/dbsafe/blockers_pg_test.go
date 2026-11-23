package dbsafe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// cicProbe is a migration as 0235 was: NO TRANSACTION, a new table and an index on it,
// then an index on a table every home has, each built CONCURRENTLY IF NOT EXISTS.
func cicProbe(name string) string {
	return "-- +goose NO TRANSACTION\n-- +goose Up\n-- +goose StatementBegin\n" +
		"CREATE TABLE IF NOT EXISTS " + name + " (id integer);\n-- +goose StatementEnd\n\n" +
		"-- +goose StatementBegin\nCREATE INDEX CONCURRENTLY IF NOT EXISTS " + name + "_id ON " + name + " (id);\n" +
		"-- +goose StatementEnd\n\n-- +goose StatementBegin\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS " + name + "_videos ON videos (created_at, id);\n" +
		"-- +goose StatementEnd\n\n-- +goose Down\n-- +goose StatementBegin\n" +
		"DROP INDEX IF EXISTS " + name + "_videos;\n-- +goose StatementEnd\n-- +goose StatementBegin\n" +
		"DROP TABLE IF EXISTS " + name + ";\n-- +goose StatementEnd\n"
}

// copyHolder is a joined machine's copy making itself anew, as on the owner's home on
// 2026-11-23: a logical walsender (its table sync) in a REPEATABLE READ transaction whose
// snapshot it holds for as long as its COPY of a large table takes.
type copyHolder struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out bytes.Buffer
}

func (r *pgRig) holdCopySnapshot(t *testing.T, role, pw string) *copyHolder {
	t.Helper()
	conn := fmt.Sprintf("host=127.0.0.1 port=%d user=%s dbname=%s replication=database application_name=%s",
		r.b.Port, role, pgbundle.AppDatabase, role)
	h := &copyHolder{cmd: exec.Command(r.t.bin("psql"), "-X", "-At", conn)}
	h.cmd.Env = append(os.Environ(), "PGPASSWORD="+pw)
	h.cmd.Stdout, h.cmd.Stderr = &h.out, &h.out
	in, err := h.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	h.in = in
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.end)
	_, _ = io.WriteString(in, "BEGIN ISOLATION LEVEL REPEATABLE READ;\nSELECT count(*) FROM videos;\nSELECT pg_sleep(600);\n")
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int
		if err := r.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'walsender'
		    AND usename = $1 AND backend_xmin IS NOT NULL`, role).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy's walsender holds no snapshot: %s", h.out.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (h *copyHolder) end() {
	_ = h.in.Close()
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	_ = h.cmd.Wait()
}

// The outages of 2026-11-14 and 2026-11-23: a migration's CREATE INDEX CONCURRENTLY
// waited on a joined machine's copy (a walsender holding a snapshot, which ending the
// other client sessions leaves alone) until its lock timeout cancelled it; the index it
// left INVALID was then taken for done by its IF NOT EXISTS at the next try. Here: the
// same wait times out, and leaves the index INVALID; with the sessions the migrations
// wait on ended after a grace (the server's database step does it while they run), the
// migration drops that index, builds it again valid, and goes on; the copy's walsender
// was what it waited on.
func TestMigrationBlockersEnded(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if !havePSQL(r.t.bin("psql")) {
		t.Fatal("no psql beside the Postgres programs")
	}
	appURL, err := r.b.URL()
	if err != nil {
		t.Fatal(err)
	}
	const role, pw = "maktaba_rep_cicprobe", "cic-probe-pw"
	if _, err := r.db.Exec(`CREATE ROLE ` + role + ` LOGIN REPLICATION PASSWORD '` + pw + `'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`GRANT SELECT ON videos TO ` + role); err != nil {
		t.Fatal(err)
	}
	invalid := func(name string) (exists, valid bool) {
		t.Helper()
		err := r.db.QueryRow(`SELECT i.indisvalid FROM pg_index i WHERE i.indexrelid = to_regclass($1)`, name).Scan(&valid)
		return err == nil, valid
	}
	dir := r.migrationsWith(t, map[string]string{
		"9101_cic_probe.sql":        cicProbe("cic_probe"),
		"9101_cic_probe.sqlite.sql": "-- +goose Up\n",
	})
	migrate := func() (string, error) {
		return runAPI(ctx, r.api, "migrate", "--dir", dir, "--dsn", appURL, "--timeout", "2m", "--lock-timeout", "3s", "up")
	}

	// as before: the migration's index waits on the copy's snapshot, times out, and is left
	h := r.holdCopySnapshot(t, role, pw)
	out, err := migrate()
	if err == nil || !strings.Contains(out, "lock timeout") {
		t.Fatalf("a CREATE INDEX CONCURRENTLY behind a copy's snapshot: %v\n%s", err, out)
	}
	left := ""
	for _, n := range []string{"cic_probe_id", "cic_probe_videos"} {
		if ok, valid := invalid(n); ok && !valid {
			left = n
		}
	}
	if left == "" {
		t.Fatalf("the cancelled build left no INVALID index:\n%s", out)
	}

	// the database step's way: what the migrations wait on is ended after a grace
	var mu sync.Mutex
	var ended []pgbundle.EndedSession
	free := func(end func(context.Context) ([]pgbundle.EndedSession, error)) func() {
		fctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for fctx.Err() == nil {
				e, err := end(fctx)
				if err != nil && fctx.Err() == nil {
					t.Errorf("end the blockers: %v", err)
					return
				}
				mu.Lock()
				ended = append(ended, e...)
				mu.Unlock()
				time.Sleep(300 * time.Millisecond)
			}
		}()
		return func() { cancel(); <-done }
	}
	stop := free(func(c context.Context) ([]pgbundle.EndedSession, error) { return r.b.EndBlockers(c, time.Second) })
	out, err = migrate()
	stop()
	if err != nil {
		t.Fatalf("with the sessions it waits on ended: %v\n%s", err, out)
	}
	if !strings.Contains(out, left) || !strings.Contains(out, "was left INVALID") {
		t.Errorf("the migration did not drop the INVALID %s it found:\n%s", left, out)
	}
	for _, n := range []string{"cic_probe_id", "cic_probe_videos"} {
		if ok, valid := invalid(n); !ok || !valid {
			t.Errorf("%s: there %v, valid %v", n, ok, valid)
		}
	}
	h.end()
	// an index a recorded migration has left INVALID (taken for done before this fix): the
	// next run builds it again from its own definition
	if _, err := r.db.Exec(`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'cic_probe_videos'::regclass`); err != nil {
		t.Fatal(err)
	}
	out, err = migrate()
	if err != nil || !strings.Contains(out, "cic_probe_videos of 9101_cic_probe.sql (recorded) was INVALID: built again") {
		t.Fatalf("a recorded migration's INVALID index: %v\n%s", err, out)
	}
	if ok, valid := invalid("cic_probe_videos"); !ok || !valid {
		t.Errorf("cic_probe_videos after its rebuild: there %v, valid %v", ok, valid)
	}
	mu.Lock()
	got := append([]pgbundle.EndedSession(nil), ended...)
	ended = nil
	mu.Unlock()
	if len(got) == 0 || got[0].Kind != "walsender" || got[0].Role != role {
		t.Fatalf("ended %+v, want the copy's walsender (%s)", got, role)
	}
	t.Logf("ended: %s", got[0])

	// a database of the user's own (Target) does the same, as the role it runs as
	dir2 := r.migrationsWith(t, map[string]string{
		"9101_cic_probe.sql": cicProbe("cic_probe"), "9101_cic_probe.sqlite.sql": "-- +goose Up\n",
		"9102_cic_probe2.sql": cicProbe("cic_probe2"), "9102_cic_probe2.sqlite.sql": "-- +goose Up\n",
	})
	h2 := r.holdCopySnapshot(t, role, pw)
	stop = free(func(c context.Context) ([]pgbundle.EndedSession, error) { return r.t.EndBlockers(c, time.Second) })
	out, err = runAPI(ctx, r.api, "migrate", "--dir", dir2, "--dsn", appURL, "--timeout", "2m", "--lock-timeout", "30s", "up")
	stop()
	h2.end()
	if err != nil {
		t.Fatalf("external: %v\n%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ended) == 0 || ended[0].Kind != "walsender" {
		t.Fatalf("external: ended %+v, want the copy's walsender", ended)
	}
	if ok, valid := invalid("cic_probe2_videos"); !ok || !valid {
		t.Errorf("cic_probe2_videos: there %v, valid %v", ok, valid)
	}
}
