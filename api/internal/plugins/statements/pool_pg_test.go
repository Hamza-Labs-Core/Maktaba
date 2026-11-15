package statements

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// The executor's connections (pool.go; 2026-11-15: the plugins' kept connections and
// transactions idle for 19 hours held the API's whole pool, and every login hung).

// executor answers another executor on the world's database, with A's statements and
// extra ones, closed with the test.
func (w *world) executor(t *testing.T, own bool, cfg Config, extra ...Statement) *Executor {
	t.Helper()
	var e *Executor
	if own {
		var err error
		if e, err = Open(w.dsn, cfg); err != nil {
			t.Fatal(err)
		}
	} else {
		e = NewWith(w.db, cfg)
	}
	t.Cleanup(e.Close)
	if err := e.Register(w.a, append(append([]Statement{}, w.reg...), extra...)); err != nil {
		t.Fatal(err)
	}
	return e
}

func (w *world) catalog() Scope {
	sc := w.scope(w.a)
	sc.SubjectKind, sc.SubjectID, sc.LibraryID = "catalog", "", ""
	return sc
}

func begin(t *testing.T, e *Executor, sc Scope) string {
	t.Helper()
	out, ref := e.Tx(context.Background(), sc, TxRequest{Op: "begin"})
	if ref != nil {
		t.Fatalf("begin: %v", ref)
	}
	return out["tx"]
}

// eventually waits up to 5 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("after 5 s: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// apiAnswers: the API's pool still runs n queries at once, each within a second.
func apiAnswers(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conns := make([]*sql.Conn, 0, n)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := 0; i < n; i++ {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("the API's connection %d of %d: %v", i+1, n, err)
		}
		conns = append(conns, c)
		var one int
		if err := c.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
			t.Fatalf("the API's query %d: %v", i+1, err)
		}
	}
}

// idleInTx counts this database's sessions idle in a transaction.
func idleInTx(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
		AND state LIKE 'idle in transaction%' AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Plugins holding every connection they may have leave the API's pool usable: on a pool of
// their own (the home), and on a pool they share, where they take at most MaxConns.
func TestRelayPluginsExhaustingTheirConnectionsLeaveTheAPIUsable(t *testing.T) {
	for _, own := range []bool{true, false} {
		name := "a pool they share"
		if own {
			name = "a pool of their own"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.db.SetMaxOpenConns(5)
			e := w.executor(t, own, Config{MaxConns: 3, SlotWait: 300 * time.Millisecond})
			sc := w.catalog()
			for i := 0; i < 3; i++ {
				begin(t, e, sc) // each holds a connection, as the incident's idle transactions did
			}
			if st := e.Stats(); st.ConnectionsHeld != 3 || st.OpenTransactions != 3 || st.OwnPool != own {
				t.Fatalf("stats %+v", st)
			}
			// a fourth waits SlotWait, then fails as retryable, never forever
			start := time.Now()
			_, ref := e.Tx(context.Background(), sc, TxRequest{Op: "begin"})
			if ref == nil || ref.Status != 503 {
				t.Fatalf("a begin past the cap: %v", ref)
			}
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("a begin past the cap waited %s", d)
			}
			if _, ref := e.Stmt(context.Background(), sc, Request{Name: "a.origin", Op: "fetch"}); ref == nil || ref.Status != 503 {
				t.Fatalf("a statement past the cap: %v", ref)
			}
			// the API keeps what the plugins may not take: all 5 on its own pool, 2 when shared
			want := 5
			if !own {
				want = 2
			}
			apiAnswers(t, w.db, want)
		})
	}
}

// The reaper rolls back an idle transaction on its timer, with no new begin to start it.
func TestRelayTimerReaperRollsBackWithoutABegin(t *testing.T) {
	w := newWorld(t)
	e := w.executor(t, false, Config{TxIdle: 300 * time.Millisecond, ReapEvery: 50 * time.Millisecond})
	sc := w.scope(w.a)
	tx := begin(t, e, sc)
	if _, ref := e.Stmt(context.Background(), sc, Request{Name: "a.insert", Op: "execute", Tx: tx,
		Args: args(w.video, nil, nil, nil, nil, nil, nil)}); ref != nil {
		t.Fatal(ref)
	}
	if st := e.Stats(); st.OpenTransactions != 1 || st.ConnectionsHeld != 1 {
		t.Fatalf("stats %+v", st)
	}
	eventually(t, "the idle transaction is still open", func() bool {
		st := e.Stats()
		return st.OpenTransactions == 0 && st.ConnectionsHeld == 0
	})
	var n int
	if err := w.db.QueryRow(`SELECT count(*) FROM fp_a`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows %d %v: the reaped transaction committed", n, err)
	}
	if n := idleInTx(t, w.db); n != 0 {
		t.Fatalf("%d sessions idle in a transaction", n)
	}
	if _, ref := e.Stmt(context.Background(), sc, Request{Name: "a.read", Op: "fetch", Tx: tx}); ref == nil || ref.Code != CodeTx {
		t.Fatalf("a call on the reaped transaction: %v", ref)
	}
}

// A call that holds a transaction and never returns blocks neither EndInvocation nor the
// reaper: the transaction leaves at once, and is rolled back when the call lets go.
func TestRelayStuckHolderBlocksNoCleanup(t *testing.T) {
	w := newWorld(t)
	e := w.executor(t, false, Config{TxIdle: 200 * time.Millisecond, TxStmtTimeout: 200 * time.Millisecond,
		ReapEvery: 50 * time.Millisecond})
	sc := w.catalog()
	stuck := func(tx string) *openTx {
		e.mu.Lock()
		ot := e.txs[tx]
		e.mu.Unlock()
		if ref := e.lockTx(ot, tx); ref != nil {
			t.Fatal(ref)
		}
		return ot
	}

	// EndInvocation
	tx := begin(t, e, sc)
	ot := stuck(tx)
	done := make(chan struct{})
	go func() { e.EndInvocation(sc.Invocation); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("EndInvocation waited for the call holding the transaction")
	}
	if st := e.Stats(); st.OpenTransactions != 0 || st.ConnectionsHeld != 1 {
		t.Fatalf("while held: %+v", st)
	}
	e.unlockTx(ot) // the call returns
	if st := e.Stats(); st.ConnectionsHeld != 0 {
		t.Fatalf("after the call returned: %+v", st)
	}

	// the reaper, past TxIdle and TxStmtTimeout
	tx = begin(t, e, sc)
	ot = stuck(tx)
	eventually(t, "the reaper left the held transaction", func() bool { return e.Stats().OpenTransactions == 0 })
	e.unlockTx(ot)
	if st := e.Stats(); st.ConnectionsHeld != 0 {
		t.Fatalf("after the call returned: %+v", st)
	}
	if n := idleInTx(t, w.db); n != 0 {
		t.Fatalf("%d sessions idle in a transaction", n)
	}
}

// A statement inside a transaction runs for at most TxStmtTimeout.
func TestRelayStatementInATransactionIsBounded(t *testing.T) {
	w := newWorld(t)
	e := w.executor(t, false, Config{TxStmtTimeout: 300 * time.Millisecond},
		Statement{Name: "a.sleep", Mode: "read", Scope: "catalog", Text: `SELECT pg_sleep(30)`})
	sc := w.catalog()
	tx := begin(t, e, sc)
	start := time.Now()
	if _, ref := e.Stmt(context.Background(), sc, Request{Name: "a.sleep", Op: "fetch", Tx: tx}); ref == nil {
		t.Fatal("a 30 s statement ran to its end")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the statement ran %s", d)
	}
	if _, ref := e.Tx(context.Background(), sc, TxRequest{Op: "rollback", Tx: tx}); ref != nil {
		t.Fatalf("rollback: %v", ref)
	}
	if st := e.Stats(); st.OpenTransactions != 0 || st.ConnectionsHeld+st.ConnectionsKept > 1 || st.ConnectionsHeld != st.ConnectionsKept {
		t.Fatalf("stats %+v: the connection was neither kept nor given back", st)
	}
	if n := idleInTx(t, w.db); n != 0 {
		t.Fatalf("%d sessions idle in a transaction", n)
	}
}

// Postgres ends a role session idle in a transaction too: each role connection sets
// idle_in_transaction_session_timeout past the reaper's own deadline, and gives it back.
func TestRelayRoleSessionsSetAnIdleInTransactionTimeout(t *testing.T) {
	w := newWorld(t)
	e := w.executor(t, false, Config{},
		Statement{Name: "a.iitst", Mode: "read", Scope: "catalog", Text: `SELECT current_setting('idle_in_transaction_session_timeout')`})
	sc := w.catalog()
	out, ref := e.Stmt(context.Background(), sc, Request{Name: "a.iitst", Op: "fetch"})
	if ref != nil {
		t.Fatal(ref)
	}
	if got, want := out.Rows[0][0], "195s"; got != want { // TxIdle 2 min + ReapEvery 15 s + 1 min
		t.Fatalf("idle_in_transaction_session_timeout %v, want %s", got, want)
	}
	e.Close()
	w.db.SetMaxOpenConns(1)
	var v string
	if err := w.db.QueryRow(`SHOW idle_in_transaction_session_timeout`).Scan(&v); err != nil || v != "0" {
		t.Fatalf("the API's pool kept the plugins' timeout: %q %v", v, err)
	}
}

// Kept connections never starve another plugin's call: a call that finds every connection
// kept gives back the oldest, and a kept connection unused for IdleKeep goes back by itself.
func TestRelayKeptConnectionsAreGivenBack(t *testing.T) {
	w := newWorld(t)
	e := w.executor(t, false, Config{MaxConns: 2, SlotWait: 5 * time.Second, IdleKeep: 300 * time.Millisecond,
		ReapEvery: 50 * time.Millisecond, TxIdle: time.Hour})
	read := Request{Name: "a.origin", Op: "fetch"}
	for _, v := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		sc := w.catalog()
		sc.Version = v // another plugin@version: connections of its own
		start := time.Now()
		if _, ref := e.Stmt(context.Background(), sc, read); ref != nil {
			t.Fatalf("%s: %v", v, ref)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("%s waited %s for a kept connection to be given back", v, d)
		}
	}
	if st := e.Stats(); st.ConnectionsKept != 2 || st.ConnectionsHeld != 2 {
		t.Fatalf("stats %+v", st)
	}
	eventually(t, "kept connections outlived IdleKeep", func() bool { return e.Stats().ConnectionsHeld == 0 })
}

// A call that found the transaction before another call committed it is refused once it
// gets it, and never ends it again: the committed connection is already given back (kept
// for the plugin's next call), so a second commit would free a slot twice and reset a
// connection another call may be using.
func TestRelayACallAfterTheCommitIsRefused(t *testing.T) {
	w := newWorld(t)
	e := w.executor(t, false, Config{})
	sc := w.catalog()
	id := begin(t, e, sc)
	ot, ref := e.tx(sc, id) // the second call's lookup, made before the commit
	if ref != nil {
		t.Fatalf("lookup: %v", ref)
	}
	if _, ref := e.Tx(context.Background(), sc, TxRequest{Op: "commit", Tx: id}); ref != nil {
		t.Fatalf("commit: %v", ref)
	}
	before := e.Stats()
	if before.ConnectionsHeld != before.ConnectionsKept {
		t.Fatalf("after the commit: %+v", before)
	}
	if ref := e.lockTx(ot, id); ref == nil || ref.Status != 409 {
		if ref == nil {
			e.unlockTx(ot)
		}
		t.Fatalf("a call on the committed transaction: %v, want 409", ref)
	}
	if after := e.Stats(); after != before {
		t.Fatalf("the refused call changed the connections: %+v, was %+v", after, before)
	}
}
