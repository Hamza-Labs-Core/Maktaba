package store

import "testing"

// #636 (slot 0185, SQLite twin): a restart request moves its seq and the revision, and
// each host reads only its own requests.
func TestRestartRequests(t *testing.T) {
	db := sqliteDB(t)
	for range 2 { // the twin applies twice
		for _, s := range upStatements(t, migration(t, "0185_component_restarts.sqlite.sql")) {
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("0185: %v\n%s", err, s)
			}
		}
	}
	st := New(db)
	rev0, err := st.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.Restarts(ctx, Home, "streaming"); err != nil || len(got) != 0 {
		t.Fatalf("no requests yet: %v %v", got, err)
	}
	for want := int64(1); want <= 2; want++ {
		seq, err := st.RequestRestart(ctx, Home, "livetv.inprocess", "streaming", "admin")
		if err != nil || seq != want {
			t.Fatalf("seq %d (%v), want %d", seq, err, want)
		}
	}
	if _, err := st.RequestRestart(ctx, Home, "queue_health", "pipeline", "admin"); err != nil {
		t.Fatal(err)
	}
	if rev, _ := st.Revision(ctx); rev != rev0+3 {
		t.Fatalf("revision %d, want %d", rev, rev0+3)
	}
	got, err := st.Restarts(ctx, Home, "streaming")
	if err != nil || len(got) != 1 || got["livetv.inprocess"] != 2 {
		t.Fatalf("streaming's requests: %v %v", got, err)
	}
	if got, _ := st.Restarts(ctx, "11111111-1111-4111-8111-111111111111", "streaming"); len(got) != 0 {
		t.Fatalf("another machine's: %v", got)
	}
	if _, err := st.RequestRestart(ctx, Home, "x", "elsewhere", "admin"); err == nil {
		t.Fatal("a host the table does not know was taken")
	}
}
