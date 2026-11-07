package statements

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// a scratch database with two first-party test plugins: A may write fp_a, B nothing.
type world struct {
	db       *sql.DB
	e        *Executor
	a, b     string
	video    string
	other    string
	library  string
	otherLib string
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func args(v ...any) []json.RawMessage {
	out := make([]json.RawMessage, len(v))
	for i, x := range v {
		out[i] = raw(x)
	}
	return out
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	w := &world{db: db, e: New(db), a: "tv.maktaba.ta" + suffix, b: "tv.maktaba.tb" + suffix}
	mustExec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`CREATE TABLE fp_a (id serial PRIMARY KEY, video_id uuid, n numeric, at timestamptz, tags text[], doc jsonb, b bytea, d interval)`)
	mustExec(`CREATE TABLE fp_secret (id int)`)
	for _, p := range []string{w.a, w.b} {
		role := pq.QuoteIdentifier(Role(p))
		mustExec(`CREATE ROLE ` + role + ` NOLOGIN`)
		mustExec(`GRANT USAGE ON SCHEMA public TO ` + role)
		t.Cleanup(func() {
			_, _ = db.Exec(`DROP OWNED BY ` + role)
			_, _ = db.Exec(`DROP ROLE IF EXISTS ` + role)
		})
	}
	ra := pq.QuoteIdentifier(Role(w.a))
	mustExec(`GRANT SELECT, INSERT, UPDATE, DELETE ON fp_a TO ` + ra)
	mustExec(`GRANT USAGE, SELECT ON SEQUENCE fp_a_id_seq TO ` + ra)
	mustExec(`GRANT SELECT ON videos TO ` + ra)
	for _, l := range []*string{&w.library, &w.otherLib} {
		if err := db.QueryRow(`INSERT INTO libraries (name, roots) VALUES ($1, '{}') RETURNING id::text`,
			"L"+suffix+fmt.Sprint(len(*l), time.Now().UnixNano())).Scan(l); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct {
		id  *string
		lib string
	}{{&w.video, w.library}, {&w.other, w.otherLib}} {
		if err := db.QueryRow(`INSERT INTO videos (library_id, path, filename, size_bytes, mtime, content_hash) VALUES ($1, $2, $2, 1, now(), md5($2) || md5($2)) RETURNING id::text`,
			v.lib, "/m/"+suffix+fmt.Sprint(time.Now().UnixNano())+".mp4").Scan(v.id); err != nil {
			t.Fatal(err)
		}
	}
	reg := []Statement{
		{Name: "a.insert", Mode: "write", Scope: "subject", ScopeArg: 1, Writes: []string{"fp_a"},
			Text: `INSERT INTO fp_a (video_id, n, at, tags, doc, b, d) VALUES ($1, $2, $3, $4, $5, $6, $7)`},
		{Name: "a.read", Mode: "read", Scope: "catalog", Reads: []string{"fp_a"},
			Text: `SELECT id, video_id, n, at, tags, doc, b, d FROM fp_a ORDER BY id`},
		{Name: "a.video", Mode: "read", Scope: "library", ScopeArg: 1, Reads: []string{"videos"},
			Text: `SELECT id, path FROM videos WHERE library_id = $1`},
		{Name: "a.secret", Mode: "write", Scope: "catalog", Writes: []string{"fp_secret"},
			Text: `INSERT INTO fp_secret (id) VALUES (1)`},
		{Name: "a.origin", Mode: "read", Scope: "catalog", Text: `SELECT current_setting('maktaba.origin') AS origin, current_user AS who`},
		{Name: "a.fail", Mode: "write", Scope: "catalog", Writes: []string{"fp_a"}, Text: `INSERT INTO fp_a (id) VALUES (999)`},
	}
	if err := w.e.Register(w.a, reg); err != nil {
		t.Fatal(err)
	}
	if err := w.e.Register(w.b, nil); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) scope(plugin string) Scope {
	return Scope{Invocation: "inv_" + plugin, PluginID: plugin, Version: "0.9.0", SubjectKind: "video",
		SubjectID: w.video, LibraryID: w.library}
}

func TestStatementsCrossPluginIsRefused(t *testing.T) {
	w := newWorld(t)
	_, err := w.e.Stmt(context.Background(), w.scope(w.b), Request{Name: "a.read", Op: "fetch"})
	if err == nil || err.Status != 403 || err.Code != CodeCapability {
		t.Fatalf("B ran A's statement: %v", err)
	}
}

func TestStatementsForgedScopeIsRefused(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	_, err := w.e.Stmt(ctx, w.scope(w.a), Request{Name: "a.insert", Op: "execute",
		Args: args(w.other, nil, nil, nil, nil, nil, nil)})
	if err == nil || err.Code != CodeScope {
		t.Fatalf("another video was written: %v", err)
	}
	_, err = w.e.Stmt(ctx, w.scope(w.a), Request{Name: "a.video", Op: "fetch", Args: args(w.otherLib)})
	if err == nil || err.Code != CodeScope {
		t.Fatalf("another library was read: %v", err)
	}
	// a library job may name any video of its library, not another's
	sc := w.scope(w.a)
	sc.SubjectKind, sc.SubjectID = "library", w.library
	if _, err := w.e.Stmt(ctx, sc, Request{Name: "a.insert", Op: "execute", Args: args(w.video, nil, nil, nil, nil, nil, nil)}); err != nil {
		t.Fatalf("its library's video: %v", err)
	}
	if _, err := w.e.Stmt(ctx, sc, Request{Name: "a.insert", Op: "execute", Args: args(w.other, nil, nil, nil, nil, nil, nil)}); err == nil || err.Code != CodeScope {
		t.Fatalf("another library's video from a library job: %v", err)
	}
}

func TestStatementsTableOutsideTheRoleIsAPermissionError(t *testing.T) {
	w := newWorld(t)
	_, err := w.e.Stmt(context.Background(), w.scope(w.a), Request{Name: "a.secret", Op: "execute"})
	if err == nil || err.Status != 403 || err.Code != CodeCapability {
		t.Fatalf("a table outside the role: %v", err)
	}
	var n int
	_ = w.db.QueryRow(`SELECT count(*) FROM fp_secret`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

func TestStatementsTransactionRollsBackOnAFailure(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	sc := w.scope(w.a)
	out, err := w.e.Tx(ctx, sc, TxRequest{Op: "begin"})
	if err != nil {
		t.Fatal(err)
	}
	tx := out["tx"]
	for i := 0; i < 2; i++ {
		if _, err := w.e.Stmt(ctx, sc, Request{Name: "a.insert", Op: "execute", Tx: tx,
			Args: args(w.video, nil, nil, nil, nil, nil, nil)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.e.Stmt(ctx, sc, Request{Name: "a.fail", Op: "execute", Tx: tx}); err != nil {
		t.Fatalf("the third write: %v", err)
	}
	if _, err := w.e.Stmt(ctx, sc, Request{Name: "a.fail", Op: "execute", Tx: tx}); err == nil {
		t.Fatal("a duplicate key did not fail")
	}
	if _, err := w.e.Tx(ctx, sc, TxRequest{Op: "rollback", Tx: tx}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = w.db.QueryRow(`SELECT count(*) FROM fp_a`).Scan(&n)
	if n != 0 || w.e.Open() != 0 {
		t.Fatalf("rows %d, open %d after the rollback", n, w.e.Open())
	}
	// another invocation cannot use a transaction it did not open
	out, _ = w.e.Tx(ctx, sc, TxRequest{Op: "begin"})
	other := w.scope(w.a)
	other.Invocation = "inv_other"
	if _, err := w.e.Stmt(ctx, other, Request{Name: "a.read", Op: "fetch", Tx: out["tx"]}); err == nil || err.Code != CodeTx {
		t.Fatalf("another invocation's transaction: %v", err)
	}
	w.e.EndInvocation(sc.Invocation)
	if w.e.Open() != 0 {
		t.Fatal("an ended invocation left its transaction open")
	}
}

func TestStatementsRunAsThePluginRoleWithItsOrigin(t *testing.T) {
	w := newWorld(t)
	out, err := w.e.Stmt(context.Background(), w.scope(w.a), Request{Name: "a.origin", Op: "fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Rows[0][0]; got != "plugin:"+w.a+"@0.9.0" {
		t.Fatalf("origin %v", got)
	}
	if got := out.Rows[0][1]; got != Role(w.a) {
		t.Fatalf("ran as %v", got)
	}
	// a role connection never goes back to the API's pool as the plugin
	w.e.Close()
	w.db.SetMaxOpenConns(1)
	var who string
	if err := w.db.QueryRow(`SELECT current_user`).Scan(&who); err != nil || who == Role(w.a) {
		t.Fatalf("the API's pool runs as %q (%v)", who, err)
	}
}

func TestStatementsReadOnlyTokenRunsNoWrite(t *testing.T) {
	w := newWorld(t)
	sc := w.scope(w.a)
	sc.ReadOnly = true
	if _, err := w.e.Stmt(context.Background(), sc, Request{Name: "a.insert", Op: "execute",
		Args: args(w.video, nil, nil, nil, nil, nil, nil)}); err == nil || err.Code != CodeCapability {
		t.Fatalf("a read-only token wrote: %v", err)
	}
}

func TestStatementsValuesRoundTrip(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	sc := w.scope(w.a)
	in := args(map[string]string{"$t": "uuid", "v": w.video}, map[string]string{"$t": "dec", "v": "12.50"},
		map[string]string{"$t": "ts", "v": "2026-11-06T10:00:00.123456Z"}, []any{"a", `b "c"`, nil},
		`{"k": [1, 2]}`, map[string]string{"$t": "bytes", "v": "AAEC"}, map[string]string{"$t": "iv", "v": "90061000000"})
	res, err := w.e.Stmt(ctx, sc, Request{Name: "a.insert", Op: "execute", Args: in})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "INSERT 0 1" {
		t.Fatalf("status %q", res.Status)
	}
	out, err := w.e.Stmt(ctx, sc, Request{Name: "a.read", Op: "fetch"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out.Rows[0][1:])
	want := `[{"$t":"uuid","v":"` + w.video + `"},{"$t":"dec","v":"12.50"},{"$t":"ts","v":"2026-11-06T10:00:00.123456Z"},` +
		`["a","b \"c\"",null],"{\"k\": [1, 2]}",{"$t":"bytes","v":"AAEC"},{"$t":"iv","v":"90061000000"}]`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestCommandTag(t *testing.T) {
	for text, want := range map[string]string{
		"UPDATE x SET a = 1":                                      "UPDATE 2",
		"  insert into x values (1)":                              "INSERT 0 2",
		"WITH d AS (DELETE FROM x RETURNING 1) SELECT * FROM d":   "SELECT 2",
		"WITH s AS (SELECT 1) UPDATE x SET a = (SELECT 1 FROM s)": "UPDATE 2",
		"DELETE FROM x":                                           "DELETE 2",
	} {
		if got := commandTag(text, 2); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	if !strings.HasPrefix(Role("tv.maktaba.core-media"), "maktaba_fp_core_media") {
		t.Fatal(Role("tv.maktaba.core-media"))
	}
}
