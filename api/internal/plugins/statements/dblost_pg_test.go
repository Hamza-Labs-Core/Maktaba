package statements

import (
	"context"
	"testing"
)

// The database restarted under a plugin's kept connection: its next statement runs on a
// new one, and the plugin sees no failure (2026-11-13: a second_listen job failed for good
// with "driver: bad connection" after the home's database was restarted).
func TestStatementsSurviveTheDatabaseClosingAKeptConnection(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	sc := w.scope(w.a)
	sc.SubjectKind, sc.SubjectID, sc.LibraryID = "catalog", "", ""
	read := Request{Name: "a.origin", Op: "fetch"}
	if _, ref := w.e.Stmt(ctx, sc, read); ref != nil {
		t.Fatalf("first read: %v", ref)
	}
	w.e.mu.Lock()
	kept := len(w.e.idle[roleKey(sc)])
	w.e.mu.Unlock()
	if kept != 1 {
		t.Fatalf("the plugin keeps %d connections, want 1", kept)
	}
	// every other session of this database ends, as on a restart
	kill := func() {
		t.Helper()
		if _, err := w.db.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()`); err != nil {
			t.Fatal(err)
		}
	}
	kill()
	out, ref := w.e.Stmt(ctx, sc, read)
	if ref != nil {
		t.Fatalf("a read after the database closed the kept connection: %d %s", ref.Status, ref)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("rows %+v", out.Rows)
	}
	// a write too: it was not run on the dead connection, and runs once on the new one
	kill()
	ins := Request{Name: "a.insert", Op: "execute", Args: args(w.video, 1, nil, nil, nil, nil, nil)}
	vs := w.scope(w.a)
	if _, ref := w.e.Stmt(ctx, vs, ins); ref != nil {
		t.Fatalf("a write after the database closed the kept connection: %d %s", ref.Status, ref)
	}
	var n int
	if err := w.db.QueryRow(`SELECT count(*) FROM fp_a`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows written: %d %v", n, err)
	}
}
