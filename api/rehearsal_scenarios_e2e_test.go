//go:build unix

package main

// The update rehearsal's scenarios (docs/update-rehearsal.md). Each is its own test, run
// by its own job of the CI's update-rehearsal matrix; each reports its downtime and fails
// above its budget, and ends with the site serving (the API, its readiness, the web, the
// API through the web, a signed-in library page).
//
//	01 a normal update with migrations, on the full seed: it commits, nothing is put back
//	02 the TV's change waits during the watch: the update commits, the TV pending
//	03 a rollback after additive migrations: the schema is kept, nothing is put back
//	04 a rollback after a destructive migration: the dump goes back the safe way, whole
//	05 that restore while machines keep logging in and asking for their copy
//	06 that restore killed (kill -9) part way: the live database untouched, back by itself
//	07 roles halted by the database step start by themselves once the record is put right
//	08 no room for the dump and the rehearsal's copy: refused before anything changes
//	09 the owner's Mac's state (a kept payload running, the app's own a failed newer one)
//	10 a joined machine never updates before its home commits

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// ---- the releases of the scenarios ----

// curSpec is this commit as the next release: its migrations and the rehearsal's
// additive one (a release with migrations whatever this commit adds).
func (k *rehearsalKit) curSpec() payloadSpec {
	n, body := additiveMigration(k.rev + 1)
	return payloadSpec{variant: "cur", version: k.cur, extra: map[string]string{n: body}}
}

// nextAdditiveFailing is this commit as the release after it, with one more additive
// migration than its API takes: it migrates, then cannot run (the injected failure).
func (k *rehearsalKit) nextAdditiveFailing() payloadSpec {
	n1, b1 := additiveMigration(k.rev + 1)
	n2, b2 := additiveNextMigration(k.rev + 2)
	return payloadSpec{variant: "next", version: k.next, extra: map[string]string{n1: b1, n2: b2}}
}

// nextDestructiveFailing is the same with a destructive migration in place of the second.
func (k *rehearsalKit) nextDestructiveFailing() payloadSpec {
	n1, b1 := additiveMigration(k.rev + 1)
	n2, b2 := destructiveMigration(k.rev + 2)
	return payloadSpec{variant: "next", version: k.next, extra: map[string]string{n1: b1, n2: b2}}
}

// profile is the scenario's seed profile (MAKTABA_REHEARSAL_PROFILE_OVERRIDE: one for
// every scenario, a quick run by hand).
func profile(def string) string {
	if o := os.Getenv("MAKTABA_REHEARSAL_PROFILE_OVERRIDE"); o != "" {
		return o
	}
	return def
}

// homeOnPrev is the seeded home on the previous release, serving.
func (r *rehearsal) homeOnPrev(prof string) (*appMachine, seedInfo) {
	r.t.Helper()
	m, info := r.seeded("home", prof)
	m.start()
	m.waitServing(r.kit.prev, 8*time.Minute)
	r.logf("the home serves %s on the %s seed", r.kit.prev, prof)
	return m, info
}

// update publishes the payload of p, offers it to the home's app, and runs the update to
// it as the owner, watching the downtime: the run, the downtime monitor (ended by the
// caller's budget), the states the record and journal took.
func (r *rehearsal) update(home *appMachine, p payloadSpec, d time.Duration) (runView, *outageMonitor, *stateLog) {
	r.t.Helper()
	dir := home.offer(p)
	r.publish(p.version, dir)
	states := home.watchStates()
	a := home.signIn()
	mon := home.watchDowntime()
	id := a.updateNow(p.version)
	run := a.follow(id, d)
	states.end()
	r.logf("the record went %v; the journal %v", states.recordStates(), states.journalStates())
	return run, mon, states
}

// ---- the database's fingerprint ----

// dbPrint is what a restore must bring back whole: the seeded tables' rows, every index,
// constraint, trigger and function of public, every grant (tables, columns, sequences,
// functions, the schema), the default privileges, the database's own grants, and the
// vectors.
type dbPrint struct {
	Counts      map[string]int64
	Indexes     map[string]string
	Constraints map[string]string
	Triggers    map[string]string
	Functions   map[string]string
	RelACL      map[string]string
	ColumnACL   map[string]string
	FuncACL     map[string]string
	SchemaACL   string
	DefaultACL  string
	DatabaseACL string
	Vectors     string
}

func fingerprint(t *testing.T, db *sql.DB) dbPrint {
	t.Helper()
	p := dbPrint{Counts: seedCounts(t, db)}
	kv := func(q string) map[string]string {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("fingerprint: %v\n%s", err, q)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var k, v sql.NullString
			if err := rows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			out[k.String] = v.String
		}
		return out
	}
	one := func(q string) string {
		var s sql.NullString
		if err := db.QueryRow(q).Scan(&s); err != nil {
			t.Fatalf("fingerprint: %v\n%s", err, q)
		}
		return s.String
	}
	acl := func(col string) string {
		return "(SELECT string_agg(a::text, ',' ORDER BY a::text) FROM unnest(" + col + ") a)"
	}
	p.Indexes = kv(`SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = 'public'`)
	p.Constraints = kv(`SELECT c.conrelid::regclass::text || '.' || c.conname, pg_get_constraintdef(c.oid)
		FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = 'public'`)
	p.Triggers = kv(`SELECT t.tgrelid::regclass::text || '.' || t.tgname, regexp_replace(pg_get_triggerdef(t.oid), '[()[:space:]]', '', 'g')
		FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE NOT t.tgisinternal AND n.nspname = 'public'`)
	p.Functions = kv(`SELECT p.oid::regprocedure::text, md5(pg_get_functiondef(p.oid)) FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public' AND p.prokind IN ('f', 'p')
		AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')`)
	p.RelACL = kv(`SELECT c.relname, ` + acl("coalesce(c.relacl, acldefault((CASE WHEN c.relkind = 'S' THEN 's' ELSE 'r' END)::\"char\", c.relowner))") + ` FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'v', 'm', 'S', 'p')`)
	p.ColumnACL = kv(`SELECT c.relname || '.' || a.attname, ` + acl("a.attacl") + ` FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND a.attacl IS NOT NULL`)
	p.FuncACL = kv(`SELECT p.oid::regprocedure::text, ` + acl("coalesce(p.proacl, acldefault('f'::\"char\", p.proowner))") + ` FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public'`)
	p.SchemaACL = one(`SELECT ` + acl("coalesce(nspacl, acldefault('n'::\"char\", nspowner))") + ` FROM pg_namespace WHERE nspname = 'public'`)
	p.DefaultACL = one(`SELECT string_agg(d, ';' ORDER BY d) FROM (SELECT defaclrole::regrole::text || ':' ||
		defaclobjtype::text || ':' || ` + acl("defaclacl") + ` AS d FROM pg_default_acl) x`)
	p.DatabaseACL = one(`SELECT ` + acl("coalesce(datacl, acldefault('d'::\"char\", datdba))") + ` FROM pg_database WHERE datname = current_database()`)
	p.Vectors = one(`SELECT count(*) || ':' || coalesce(sum(('x' || substr(md5(embedding::text), 1, 8))::bit(32)::int::bigint), 0)
		FROM segment_embeddings`)
	return p
}

// diff lists what b lacks or has otherwise than a (at most 40 lines).
func (a dbPrint) diff(b dbPrint) []string {
	var out []string
	add := func(s string) {
		if len(out) < 40 {
			out = append(out, s)
		}
	}
	for _, k := range sortedKeysOf(a.Counts) {
		if a.Counts[k] != b.Counts[k] {
			add(fmt.Sprintf("rows of %s: %d before, %d after", k, a.Counts[k], b.Counts[k]))
		}
	}
	maps := []struct {
		what string
		a, b map[string]string
	}{{"index", a.Indexes, b.Indexes}, {"constraint", a.Constraints, b.Constraints}, {"trigger", a.Triggers, b.Triggers},
		{"function", a.Functions, b.Functions}, {"grant on", a.RelACL, b.RelACL}, {"column grant on", a.ColumnACL, b.ColumnACL},
		{"function grant on", a.FuncACL, b.FuncACL}}
	for _, m := range maps {
		for _, k := range sortedKeysOf(m.a) {
			if v, ok := m.b[k]; !ok {
				add(fmt.Sprintf("%s %s is missing", m.what, k))
			} else if v != m.a[k] {
				add(fmt.Sprintf("%s %s: %q before, %q after", m.what, k, m.a[k], v))
			}
		}
		for _, k := range sortedKeysOf(m.b) {
			if _, ok := m.a[k]; !ok {
				add(fmt.Sprintf("%s %s was not there before", m.what, k))
			}
		}
	}
	for _, s := range []struct{ what, a, b string }{{"the schema's grants", a.SchemaACL, b.SchemaACL},
		{"the default privileges", a.DefaultACL, b.DefaultACL}, {"the database's grants", a.DatabaseACL, b.DatabaseACL},
		{"the vectors (count:checksum)", a.Vectors, b.Vectors}} {
		if s.a != s.b {
			add(fmt.Sprintf("%s: %q before, %q after", s.what, s.a, s.b))
		}
	}
	return out
}

func (a dbPrint) summary() string {
	return fmt.Sprintf("%d indexes, %d constraints, %d triggers, %d functions, %d table grants, %d column grants, "+
		"%d function grants, database grants %q, vectors %s", len(a.Indexes), len(a.Constraints), len(a.Triggers),
		len(a.Functions), len(a.RelACL), len(a.ColumnACL), len(a.FuncACL), a.DatabaseACL, a.Vectors)
}

// ---- assertions shared by the scenarios ----

func (r *rehearsal) countsUnchanged(before, after map[string]int64) {
	r.t.Helper()
	for _, k := range sortedKeysOf(before) {
		if before[k] != after[k] {
			r.t.Errorf("rows of %s: %d before the update, %d after", k, before[k], after[k])
		}
	}
}

// noRestore: the database's record never said restoring or restored, and no dump was put
// into the library database.
func (r *rehearsal) noRestore(m *appMachine, states *stateLog) {
	r.t.Helper()
	for _, s := range states.recordStates() {
		if s == "restoring" || s == "restored" {
			r.t.Errorf("a restore ran: the database's record went %v", states.recordStates())
			break
		}
	}
	for _, line := range strings.Split(m.log.String(), "\n") {
		if strings.Contains(line, "into a fresh database of its own") || strings.Contains(line, "putting the database back") ||
			strings.Contains(line, "is put back whole") {
			r.t.Errorf("a restore ran: %s", line)
			break
		}
	}
}

func rehearsalTableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(`SELECT to_regclass($1)::text`, name).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s.Valid
}

// setUpdatePolicy sets keys of the update policy (rules.updates) in the database.
func setUpdatePolicy(t *testing.T, db *sql.DB, keys map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(keys)
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.updates', $1::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = app_settings.value || EXCLUDED.value, updated_at = now()`, string(raw)); err != nil {
		t.Fatal(err)
	}
}

// =====================================================================================
// 01 A normal update with migrations, on the full seed.
// =====================================================================================

func TestUpdateRehearsal01NormalUpdate(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	prof := profile("full")
	home, seed := r.homeOnPrev(prof)
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	r.countsUnchanged(seed.Counts, before)
	if prof == "full" {
		// as on the owner's home (47 GB): a database above rehearse_max_db_gb skips the
		// rehearsal on a scratch copy (the backup still runs); the other scenarios rehearse
		setUpdatePolicy(t, db, map[string]any{"rehearse_max_db_gb": 1})
	}
	run, mon, states := r.update(home, k.curSpec(), 40*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)", k.cur, run.State, run.Error, run.Steps)
	}
	home.waitServing(k.cur, 3*time.Minute)
	r.budget(mon, "a normal update (previous release -> this commit, with migrations)", 2*time.Minute)
	home.assertServing(k.cur)
	r.countsUnchanged(before, seedCounts(t, db))
	r.noRestore(home, states)
	rec, _ := home.record()
	if rec.State != "migrated" || rec.Version != k.cur {
		t.Errorf("the database's record is %+v, want migrated for %s", rec, k.cur)
	}
	if !rehearsalTableExists(t, db, "rehearsal_probe") {
		t.Error("the update's migration did not run (no rehearsal_probe)")
	}
	if n := queryInt(t, db, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`); n != int64(k.rev+1) {
		t.Errorf("the schema is at %d, want %d", n, k.rev+1)
	}
	if j := home.journal(); j.State != "committed" {
		t.Errorf("the home's journal says %s", j.State)
	}
}

// =====================================================================================
// 02 The TV's change waits during the watch.
// =====================================================================================

// The live TV airs a release (staging is on by default), so the update's TV step asks for
// a change of the streaming binary; its staging is a pipeline job no worker here runs: the
// change waits (a slow staging) all through the watch, as the outage's change #57 waited
// for an approval. The release must commit when its watch passed, with the TV pending (or
// held), never rolled back by the TV's waiting (watch_timeout, tv_timeout).
func TestUpdateRehearsal02WatchWithTVWaiting(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	if !tvSlotPortsFree() {
		r.skipUnlessRequired("the TV slots' fixed ports (8791-8793, 9111-9113) are taken: another Maktaba airs TV on " +
			"this machine, and the rehearsal's TV would meet it")
	}
	// the TV on, as on the owner's home: the update's TV step runs (story 35.6)
	home, _ := r.seeded("home", profile("restore"))
	home.tvSlots = "on"
	home.start()
	home.waitServing(k.prev, 8*time.Minute)
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	if _, err := db.Exec(`INSERT INTO tv_releases (id, digest, kinds, sched_gen, streaming_version, streaming_sha256, created_by)
		VALUES ('7e1e0000-0000-4000-8000-000000000001', 'rehearsal-live', '["binary"]', 1, $1, $2, 'rehearsal')`,
		k.prev, strings.Repeat("ab", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch, switched_at)
		VALUES ('cluster', '7e1e0000-0000-4000-8000-000000000001', 1, 1, now())
		ON CONFLICT (scope) DO UPDATE SET live_release_id = EXCLUDED.live_release_id, live_gen = 1`); err != nil {
		t.Fatal(err)
	}
	run, mon, states := r.update(home, k.curSpec(), 25*time.Minute)
	for _, s := range states.journalStates() {
		if s == "rolling_back" || s == "rolled_back" {
			t.Errorf("the release was rolled back while its TV change waited (journal %v)", states.journalStates())
		}
	}
	if strings.Contains(home.log.String(), "watch_timeout") || strings.Contains(string(run.Error), "watch_timeout") {
		t.Error("the watch timed out while the TV waited (watch_timeout)")
	}
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s", k.cur, run.State, run.Error)
	}
	j := home.journal()
	tvOutcome := ""
	if j.TV != nil {
		tvOutcome = j.TV.Outcome
	}
	var detail struct {
		TV struct {
			Outcome string `json:"outcome"`
		} `json:"tv"`
	}
	_ = json.Unmarshal(run.Detail, &detail)
	r.logf("the TV step: journal %q, run detail %q", tvOutcome, detail.TV.Outcome)
	if tvOutcome != "pending" && tvOutcome != "held" {
		t.Errorf("the TV step ended %q, want pending or held", tvOutcome)
	}
	home.waitServing(k.cur, 3*time.Minute)
	r.budget(mon, "an update whose TV change waits", 2*time.Minute)
	home.assertServing(k.cur)
	r.countsUnchanged(before, seedCounts(t, db))
	r.noRestore(home, states)
}

// =====================================================================================
// 03 A rollback after additive migrations keeps the schema.
// =====================================================================================

// This commit, installed, updates to the release after it, whose migrations are all
// additive but whose API refuses the database it migrated (a health failure after the
// migrations). The rollback must put nothing back: this commit runs again on the schema
// the newer release made (kept for it), the site serves.
func TestUpdateRehearsal03RollbackAdditive(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.homeOnPrev(profile("restore"))
	// the release before the failing one is this commit: a rollback lands on a release
	// that knows how to run on a kept schema (releases before 2026-10-28 do not, and
	// always put the dump back)
	home.installByHand(k.curSpec())
	home.waitServing(k.cur, 10*time.Minute)
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	run, mon, states := r.update(home, k.nextAdditiveFailing(), 25*time.Minute)
	if run.State != "rolled_back" {
		t.Fatalf("the update to %s ended %s, want rolled_back: %s", k.next, run.State, run.Error)
	}
	home.waitServing(k.cur, 5*time.Minute)
	r.budget(mon, "a rollback after additive migrations", 4*time.Minute)
	home.assertServing(k.cur)
	r.noRestore(home, states)
	rec, _ := home.record()
	if rec.KeptFor != k.cur {
		t.Errorf("the database's record is %+v: not kept for %s", rec, k.cur)
	}
	if !rehearsalTableExists(t, db, "rehearsal_probe_next") {
		t.Error("the newer release's schema was not kept (rehearsal_probe_next is gone: the dump was put back)")
	}
	r.countsUnchanged(before, seedCounts(t, db))
}

// =====================================================================================
// 04 A rollback after a destructive migration puts the dump back, whole.
// =====================================================================================

func TestUpdateRehearsal04RollbackDestructive(t *testing.T) {
	r := newRehearsal(t)
	r.destructiveRollback(destructiveOptions{})
}

// =====================================================================================
// 05 The restore while machines keep logging in.
// =====================================================================================

// The outage's restore failed with "tuple concurrently updated": a joined machine kept
// logging in to the home's database and asking for its copy (cluster_node_replica_grant:
// GRANT … TO maktaba_replicas on every table, ALTER PUBLICATION) while pg_restore granted
// the same tables. Here three machines do that all through the update and its rollback.
func TestUpdateRehearsal05RestoreUnderLogins(t *testing.T) {
	r := newRehearsal(t)
	r.destructiveRollback(destructiveOptions{logins: true})
}

// =====================================================================================
// 06 The restore killed part way.
// =====================================================================================

// kill -9 of the server (and its pg_restore) while the dump is put back: the library
// database in place must be untouched, and the site must come back by itself (the next
// start finishes or redoes the restore), with nothing done by hand.
func TestUpdateRehearsal06RestoreKilled(t *testing.T) {
	r := newRehearsal(t)
	r.destructiveRollback(destructiveOptions{kill: true})
}

type destructiveOptions struct {
	logins bool // machines log in and ask for their copy throughout
	kill   bool // kill -9 the server and pg_restore mid-restore
}

func (r *rehearsal) destructiveRollback(o destructiveOptions) {
	t, k := r.t, r.kit
	t.Helper()
	home, seed := r.homeOnPrev(profile("restore"))
	db := home.superDB("maktaba")
	before := fingerprint(t, db)
	r.logf("before: %s", before.summary())
	var logins *machineLogins
	if o.logins {
		logins = home.machineLogins(seed.Machines, db)
	}
	var killed chan string
	if o.kill {
		killed = make(chan string, 1)
		go func() { killed <- home.killDuringRestore(40 * time.Minute) }()
	}
	run, mon, states := r.update(home, k.nextDestructiveFailing(), 40*time.Minute)
	if run.State != "rolled_back" {
		t.Errorf("the update to %s ended %s, want rolled_back: %s", k.next, run.State, run.Error)
	}
	if o.kill {
		select {
		case what := <-killed:
			r.logf("killed mid-restore: %s", what)
			if what == "" {
				t.Fatal("no restore was seen to kill")
			}
		case <-time.After(10 * time.Minute):
			t.Fatal("no restore was seen to kill")
		}
	}
	// the site comes back by itself, on the previous release, with the dump put back
	// (the record says restored once the dump is back, then migrated once the previous
	// release's database step checked it; a release from before 2026-10-28 goes from
	// restoring to migrated): whether the dump came back whole, the fingerprint says
	waitFor(t, "the home back on "+k.prev+" with the dump put back", 30*time.Minute, func() bool {
		rec, _ := home.record()
		return home.selfVersion() == k.prev && home.ready() && rec.Version == k.prev &&
			(rec.State == "restored" || rec.State == "migrated")
	})
	budget := 10 * time.Minute
	if o.kill {
		budget = 20 * time.Minute
	}
	r.budget(mon, "a rollback that puts the dump back", budget)
	if logins != nil {
		logins.end(r)
	}
	home.assertServing(k.prev)
	if !o.kill || !strings.Contains(home.log.String(), "level=error") {
		// the safe path: a database of its own, checked whole, renamed into place
		for _, want := range []string{"into a fresh database of its own", "is put back whole"} {
			if !strings.Contains(home.log.String(), want) {
				t.Errorf("the restore did not take the safe path (scratch database, check, rename): no %q in the log", want)
			}
		}
	}
	after := fingerprint(t, db)
	r.logf("after: %s", after.summary())
	if d := before.diff(after); len(d) > 0 {
		t.Errorf("the database put back is not as it was:\n  %s", strings.Join(d, "\n  "))
	}
	if rehearsalTableExists(t, db, "rehearsal_probe") {
		t.Error("the failed release's migrations are still there after the restore (rehearsal_probe)")
	}
	r.logf("the record went %v", states.recordStates())
}

// machineLogins are fake joined machines logging in to the home's database again and
// again, each asking for its heartbeat and its copy's grant.
type machineLogins struct {
	stop     chan struct{}
	wg       sync.WaitGroup
	ok, fail atomic.Int64
	mu       sync.Mutex
	errs     map[string]int
}

func (m *appMachine) machineLogins(machines []seedMachine, super *sql.DB) *machineLogins {
	t := m.r.t
	l := &machineLogins{stop: make(chan struct{}), errs: map[string]int{}}
	for _, mc := range machines {
		var verifier string
		if err := super.QueryRow(`SELECT rolpassword FROM pg_authid WHERE rolname = $1`, mc.Role).Scan(&verifier); err != nil {
			t.Fatal(err)
		}
		mc := mc
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			for {
				select {
				case <-l.stop:
					return
				default:
				}
				err := func() error {
					dsn := (&url.URL{Scheme: "postgres", User: url.UserPassword(mc.Role, mc.Password),
						Host: fmt.Sprintf("127.0.0.1:%d", m.pg), Path: "/maktaba",
						RawQuery: "sslmode=disable&connect_timeout=2"}).String()
					d, err := sql.Open("postgres", dsn)
					if err != nil {
						return err
					}
					defer d.Close()
					if _, err := d.Exec(`SELECT * FROM cluster_node_beat($1)`, mc.ID); err != nil {
						return err
					}
					_, err = d.Exec(`SELECT cluster_node_replica_grant($1, $2, false)`, mc.ID, verifier)
					return err
				}()
				if err != nil {
					l.fail.Add(1)
					msg := err.Error()
					if len(msg) > 90 {
						msg = msg[:90]
					}
					l.mu.Lock()
					l.errs[msg]++
					l.mu.Unlock()
				} else {
					l.ok.Add(1)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}()
	}
	m.r.logf("%d machines log in and ask for their copy, again and again", len(machines))
	return l
}

func (l *machineLogins) end(r *rehearsal) {
	close(l.stop)
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	var errs []string
	for _, k := range sortedKeysOf(l.errs) {
		errs = append(errs, fmt.Sprintf("%dx %s", l.errs[k], k))
	}
	sort.Strings(errs)
	r.logf("the machines' logins: %d asked for their copy, %d failed (%s)", l.ok.Load(), l.fail.Load(), strings.Join(errs, "; "))
	if l.ok.Load() == 0 {
		r.t.Error("no machine ever logged in: the logins did not run")
	}
	// the outage's mechanism: a machine's login reached the library database while it was
	// put back, and its grants met pg_restore's. A restore that keeps every other login out
	// until it is whole leaves no machine a database without its tables or functions.
	half := 0
	for msg, n := range l.errs {
		if strings.Contains(msg, "does not exist") && !strings.Contains(msg, "pq: database ") {
			half += n
		}
	}
	if half > 0 {
		r.t.Errorf("%d machine logins reached the library database while it was being put back (its tables or "+
			"functions not there yet): the restore did not keep the other logins out", half)
	}
}

// killDuringRestore waits for a pg_restore of this machine's dump and kills it with the
// server that runs it (kill -9 both). It says what it killed ("" when it saw none).
func (m *appMachine) killDuringRestore(d time.Duration) string {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("pgrep", "-f", "pg_restore").Output()
		for _, f := range strings.Fields(string(out)) {
			pid, _ := strconv.Atoi(f)
			cmdline := processCommand(pid)
			// the rollback's restore of the dump taken before the update
			// (backups/pre-<from>-to-<to>.dump), never the rehearsal's scratch copy
			if !strings.Contains(cmdline, m.root) || !strings.Contains(cmdline, "/backups/pre-") ||
				strings.Contains(cmdline, "rehearse") {
				continue
			}
			// let it write part of the database first
			time.Sleep(time.Second)
			ppid := parentPID(pid)
			parent := processCommand(ppid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
			what := fmt.Sprintf("pg_restore %d (%s)", pid, tail(cmdline, 160))
			fmt.Fprintf(m.logFile, "\n==== rehearsal: kill -9 %s ====\n", what)
			// the database in place, right after (its Postgres still up): untouched by
			// the restore cut short
			m.checkUntouched()
			if strings.Contains(parent, "maktaba-server") {
				_ = syscall.Kill(ppid, syscall.SIGKILL)
				what += fmt.Sprintf(", then its server %d (%s)", ppid, tail(parent, 160))
				fmt.Fprintf(m.logFile, "\n==== rehearsal: kill -9 the server %d ====\n", ppid)
			}
			return what
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ""
}

// checkUntouched: the library database in place is still the one the failed release
// migrated (its own migrations there, its rows), not a part of the dump.
func (m *appMachine) checkUntouched() {
	t := m.r.t
	d, err := sql.Open("postgres", m.dsn("maktaba_admin", "maktaba"))
	if err != nil {
		t.Error(err)
		return
	}
	defer d.Close()
	var probe sql.NullString
	var videos int64
	err = d.QueryRow(`SELECT to_regclass('rehearsal_probe')::text, (SELECT count(*) FROM videos)`).Scan(&probe, &videos)
	switch {
	case err != nil:
		t.Errorf("the library database in place cannot be read right after the kill: %v", err)
	case !probe.Valid:
		t.Errorf("the library database in place was changed by the restore cut short (the failed release's " +
			"rehearsal_probe is gone: the database was replaced in place)")
	default:
		m.r.logf("the library database in place is untouched by the restore cut short (%d videos, rehearsal_probe there)", videos)
	}
}

func processCommand(pid int) string {
	out, _ := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(out))
}

func parentPID(pid int) int {
	out, _ := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// =====================================================================================
// 07 Halted roles start by themselves once the record is put right.
// =====================================================================================

// The outage left every role halted by the database step (the restore had failed); the
// owner put the record right by hand, and nothing started until the server restarted.
// Here the home on this commit finds a restore it cannot do (its dump is gone): its roles
// halt. The record is then written restored, as the owner did: the roles must start with
// no restart of the server.
func TestUpdateRehearsal07HaltRecovers(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.homeOnPrev(profile("small"))
	home.installByHand(k.curSpec())
	home.waitServing(k.cur, 10*time.Minute)
	home.stop()
	recPath := filepath.Join(home.root, "state", "database.json")
	writeRec := func(v map[string]any) {
		v["at"] = time.Now().UTC().Format(time.RFC3339Nano)
		raw, _ := json.MarshalIndent(v, "", "  ")
		tmp := recPath + ".rehearsal"
		if err := os.WriteFile(tmp, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, recPath); err != nil {
			t.Fatal(err)
		}
	}
	gone := filepath.Join(home.root, "backups", "pre-"+k.cur+"-to-0.0.1.dump")
	writeRec(map[string]any{"version": "0.0.1", "state": "restoring", "from": k.cur, "dump": gone, "failed": "0.0.1",
		"error": "the rehearsal's failed update"})
	home.start()
	waitFor(t, "the roles halted by the database step", 5*time.Minute, func() bool {
		return strings.Contains(home.log.String(), "cannot start: the update to 0.0.1")
	})
	time.Sleep(10 * time.Second)
	if home.ready() {
		t.Fatalf("the API serves although the database step halted")
	}
	serve := home.servePID()
	r.logf("the roles are halted (the server is %d): the record is put right by hand", serve)
	fixed := time.Now()
	writeRec(map[string]any{"version": k.cur, "state": "restored", "from": k.cur})
	mon := home.watchDowntime()
	ok := false
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if home.selfVersion() == k.cur && home.ready() {
			ok = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	took := time.Since(fixed)
	_, _ = mon.end()
	r.logf("DOWNTIME after the record was put right: %s (budget 1m)", took.Round(100*time.Millisecond))
	r.summary(fmt.Sprintf("| %s | the halted roles start once the record is right | %s | 1m |", t.Name(), took.Round(time.Second)))
	if !ok {
		t.Fatalf("the halted roles did not start within 3 minutes of the record put right (no restart)")
	}
	if took > time.Minute {
		t.Errorf("the halted roles took %s to start (budget 1m)", took.Round(time.Second))
	}
	if now := home.servePID(); serve != 0 && now != serve {
		t.Errorf("the server restarted (%d, then %d): the halt had no way out by itself", serve, now)
	}
	home.assertServing(k.cur)
}

// servePID is the pid of the running `maktaba-server serve` of this machine (0: none).
func (m *appMachine) servePID() int {
	out, _ := exec.Command("pgrep", "-f", "maktaba-server serve").Output()
	for _, f := range strings.Fields(string(out)) {
		pid, _ := strconv.Atoi(f)
		if strings.Contains(processCommand(pid), m.dir) {
			return pid
		}
	}
	return 0
}

// =====================================================================================
// 08 No room for two copies.
// =====================================================================================

// The home's state is on a small volume (MAKTABA_REHEARSAL_SMALL_VOLUME: the CI job mounts
// one) with too little room for the update's dump and the rehearsal's scratch copy beside
// the database. The documented behaviour (install-kinds.md, "No room"): the run's
// preflight refuses with disk_space before anything is fetched, dumped or switched; the
// home goes on serving its release, the database untouched.
func TestUpdateRehearsal08NoRoom(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	vol := os.Getenv("MAKTABA_REHEARSAL_SMALL_VOLUME")
	if vol == "" {
		r.skipUnlessRequired("MAKTABA_REHEARSAL_SMALL_VOLUME is not set: a volume of its own (never this machine's disk) is filled here")
	}
	seedDir := filepath.Join(seedsDir(r), seedKey(r, "small"))
	if _, ok := readSeed(seedDir); !ok {
		r.makeSeed("small", seedDir)
	}
	home := r.machinePaths("home", filepath.Join(vol, "home-machine"))
	cloneTree(t, filepath.Join(seedDir, "machine"), home.dir)
	home.start()
	home.waitServing(k.prev, 8*time.Minute)
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	// leave 1.5 GiB free: less than a release (2 GiB) and a dump plus the rehearsal's copy
	// with 1 GiB to spare, more than the running home needs
	var st syscall.Statfs_t
	if err := syscall.Statfs(vol, &st); err != nil {
		t.Fatal(err)
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	balloon := free - 3<<29
	if balloon > 0 {
		f, err := os.Create(filepath.Join(vol, "balloon"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(0); err != nil {
			t.Fatal(err)
		}
		// written, not sparse: the room is really taken
		buf := make([]byte, 8<<20)
		for left := balloon; left > 0; left -= int64(len(buf)) {
			n := int64(len(buf))
			if left < n {
				n = left
			}
			if _, err := f.Write(buf[:n]); err != nil {
				break
			}
		}
		_ = f.Close()
		t.Cleanup(func() { _ = os.Remove(filepath.Join(vol, "balloon")) })
	}
	_ = syscall.Statfs(vol, &st)
	r.logf("the volume has %d MB free", int64(st.Bavail)*int64(st.Bsize)>>20)
	recBefore, _ := home.record()
	run, mon, states := r.update(home, k.curSpec(), 15*time.Minute)
	r.budget(mon, "an update refused for room", 5*time.Second)
	if run.State == "done" {
		t.Fatalf("the update ran with no room for the dump and the rehearsal's copy")
	}
	if !strings.Contains(string(run.Error), "disk_space") && !strings.Contains(home.log.String(), "disk_space") {
		t.Errorf("the run ended %s without saying disk_space: %s", run.State, run.Error)
	}
	for _, s := range states.journalStates() {
		if s == "applying" || s == "restarting" || s == "watching" {
			t.Errorf("the home switched although it had no room (journal %v)", states.journalStates())
		}
	}
	recAfter, _ := home.record()
	if recAfter.State != recBefore.State || recAfter.Version != recBefore.Version {
		t.Errorf("the database's record changed: %+v, then %+v", recBefore, recAfter)
	}
	home.assertServing(k.prev)
	r.countsUnchanged(before, seedCounts(t, db))
}

// =====================================================================================
// 09 The owner's Mac's state.
// =====================================================================================

// As the owner's Mac was left on 2026-10-28: the app holds a newer release that failed
// (the app's own payload), the launcher runs the kept copy of the release before
// (updates/payload-override, with payload-override.failed naming the failed one), the
// database's record put right by hand. The update to this commit must go through.
func TestUpdateRehearsal09FromTheLiveState(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.seeded("home", profile("small"))
	// the kept copy of the previous release (as the update's stage keeps it), the app's
	// own payload the failed newer release
	kept := filepath.Join(home.root, "updates", "payloads", k.prev)
	_ = os.MkdirAll(filepath.Dir(kept), 0o700)
	if err := copyDir(home.payload, kept); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(kept, ".kept"), []byte(k.prev+"\n"), 0o600)
	_ = os.RemoveAll(home.payload)
	r.kit.makePayload(t, home.payload, payloadSpec{variant: "prev", version: k.failed})
	_ = os.WriteFile(filepath.Join(home.root, "updates", "payload-override"), []byte(kept+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(home.root, "updates", "payload-override.failed"), []byte(k.failed+"\n"), 0o600)
	rec, all := home.record()
	all["state"], all["from"], all["failed"] = "restored", k.prev, k.failed
	all["error"] = "the rehearsal: the owner's Mac on 2026-10-28"
	raw, _ := json.MarshalIndent(all, "", "  ")
	_ = os.WriteFile(filepath.Join(home.root, "state", "database.json"), raw, 0o644)
	r.logf("the live state: the app holds %s (failed), the launcher runs the kept %s, the record %s → restored", k.failed,
		k.prev, rec.State)
	home.start()
	home.waitServing(k.prev, 8*time.Minute)
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	run, mon, states := r.update(home, k.curSpec(), 25*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update from the live state to %s ended %s: %s", k.cur, run.State, run.Error)
	}
	home.waitServing(k.cur, 3*time.Minute)
	r.budget(mon, "the update from the owner's Mac's state", 2*time.Minute)
	home.assertServing(k.cur)
	r.countsUnchanged(before, seedCounts(t, db))
	r.noRestore(home, states)
	if o := home.readFile(filepath.Join("updates", "payload-override")); strings.TrimSpace(o) != "" {
		t.Errorf("the payload override is still there: %q", o)
	}
	if v := payloadJSONVersion(home.payload); v != k.cur {
		t.Errorf("the app's payload is %q, want %s", v, k.cur)
	}
}

func payloadJSONVersion(dir string) string {
	var p struct {
		Version string `json:"version"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "payload.json"))
	_ = json.Unmarshal(b, &p)
	return strings.TrimPrefix(p.Version, "v")
}

// joinNode installs a second Mac app on the previous release and joins it to home as a
// full machine (its login, its row approved, its node.json), its copy of the home's
// database on its own bundled Postgres; env is more of the machine's environment.
func (r *rehearsal) joinNode(home *appMachine, name string, env ...string) *appMachine {
	t, k := r.t, r.kit
	node := r.newAppMachine(name, payloadSpec{variant: "prev", version: k.prev})
	node.env = env
	node.ports()
	homePriv, err := os.ReadFile(filepath.Join(home.root, "cluster.key"))
	if err != nil {
		t.Fatalf("the home's cluster key: %v", err)
	}
	homePub, err := cluster.PublicKey(decodeKey(t, homePriv))
	if err != nil {
		t.Fatal(err)
	}
	nodePriv, nodePub, _ := cluster.GenerateKey()
	nodeID := uuid.NewString()
	role := cluster.RoleName(nodeID)
	db := home.superDB("maktaba")
	if err := (&cluster.PGRoles{DB: db}).Create(t.Context(), role, "node-pass-0123456789"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role,
	        approved_at, approved_by, full_node, full_capable, full_allowed, web_url)
	        VALUES ($1, $2, 'approved', $3, $4, $5, $6, now(), 'rehearsal', true, true, true, $7)`,
		nodeID, name, runtime.GOOS, runtime.GOARCH, cluster.EncodeKey(nodePub), role, node.webURL()); err != nil {
		t.Fatal(err)
	}
	dsn := (&url.URL{Scheme: "postgres", User: url.UserPassword(role, "node-pass-0123456789"),
		Host: fmt.Sprintf("127.0.0.1:%d", home.pg), Path: "/maktaba", RawQuery: "sslmode=prefer"}).String()
	cfg, _ := json.Marshal(map[string]any{"node_id": nodeID, "name": name, "private_key": cluster.EncodeKey(nodePriv),
		"home":         map[string]any{"url": home.apiURL(), "cluster_pub": cluster.EncodeKey(homePub)},
		"database_url": dsn})
	if err := os.WriteFile(filepath.Join(node.root, "node.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	node.pgExtra = fmt.Sprintf("copy_port = %d", freePort(t))
	node.tomlTail = "\n[database]\nurl = \"sqlite://unused.db\"\n"
	node.start()
	waitFor(t, "the machine on "+k.prev+" with its agent", 5*time.Minute, func() bool {
		s := node.selfJSON()
		return strings.TrimPrefix(fmt.Sprint(s["version"]), "v") == k.prev && s["agent_connected"] == true
	})
	r.logf("the machine %s joined the home (%s)", name, nodeID)
	return node
}

// decodeKey reads a key file: the raw key, or its base64.
func decodeKey(t *testing.T, b []byte) []byte {
	t.Helper()
	if len(b) == cluster.KeySize {
		return b // the raw key, as the home writes it
	}
	k, err := cluster.DecodeKey(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("the home's cluster key: %v", err)
	}
	return k
}

func (m *appMachine) selfJSON() map[string]any {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(m.webURL() + "/api/cluster/self")
	if err != nil {
		return map[string]any{}
	}
	defer resp.Body.Close()
	out := map[string]any{}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return out
}

// assertNodeServing: the machine answers with version, its API ready, nothing of it paused.
func (m *appMachine) assertNodeServing(version string) {
	t := m.r.t
	t.Helper()
	s := m.selfJSON()
	if v := strings.TrimPrefix(fmt.Sprint(s["version"]), "v"); v != version {
		t.Errorf("%s answers as %q, want %s", m.name, v, version)
	}
	// its API ready (a moment after its home put a dump back: its copy is made anew)
	readyBy := time.Now().Add(3 * time.Minute)
	for !m.ready() && time.Now().Before(readyBy) {
		time.Sleep(time.Second)
	}
	if !m.ready() {
		t.Errorf("%s's API is not ready", m.name)
	}
	raw, _ := json.Marshal(s)
	low := strings.ToLower(string(raw))
	if strings.Contains(low, `"paused":true`) || strings.Contains(low, `"state":"paused"`) {
		t.Errorf("%s is paused: %s", m.name, tail(string(raw), 600))
	}
	m.r.logf("%s serves %s, not paused", m.name, version)
}
