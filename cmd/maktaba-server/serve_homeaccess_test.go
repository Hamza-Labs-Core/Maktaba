package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
)

// The home re-makes this machine's database access over the channel (its role made again
// on the home's current database, a new password, the database's new port): node.json
// keeps it, pointed at the host this machine reaches the home at, its other fields and the
// url's settings kept, and the machine starts again with it; the same access changes
// nothing; a bad one is refused.
func TestHomeAccessOverTheChannel(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node.json")
	t.Setenv("MAKTABA_NODE_CONFIG", node)
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	if err := os.WriteFile(node, []byte(`{"node_id":"n1","name":"studio","private_key":"k",
	  "home":{"url":"http://192.168.50.177:8080"},
	  "database_url":"postgres://maktaba_node_1e943eda:old@192.168.50.177:5432/maktaba?sslmode=prefer",
	  "database_tls":"off","joined_at":"2026-10-01T10:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := homeAccessReloadAfter
	homeAccessReloadAfter = 0
	t.Cleanup(func() { homeAccessReloadAfter = old })
	for len(reloadCh) > 0 {
		<-reloadCh
	}
	s := newSwitching(config.Config{})
	healed := atomic.Int32{}
	s.homeDB = &homeDBWatch{onChange: func() { healed.Add(1) }}
	ca := testCAPEM(t)
	args, _ := json.Marshal(map[string]any{"role": "maktaba_node_1e943eda", "password": "fresh-pass", "db_port": 54329,
		"db_name": "maktaba", "database_ca": ca})
	handled, ok, detail := s.command(t.Context(), "home.access", args)
	if !handled || !ok {
		t.Fatalf("the access: %v %v", ok, detail)
	}
	n := config.LocalNodeConfig()
	u, err := url.Parse(n.DatabaseURL)
	if err != nil || u.Host != "192.168.50.177:54329" || u.User.Username() != "maktaba_node_1e943eda" ||
		u.Query().Get("sslmode") != "prefer" || u.Path != "/maktaba" {
		t.Fatalf("node.json's database: %s (%v)", n.DatabaseURL, err)
	}
	if pw, _ := u.User.Password(); pw != "fresh-pass" || n.DatabaseCA != ca || n.DatabaseTLS != "" {
		t.Fatalf("node.json's access: %+v", n)
	}
	b, _ := os.ReadFile(node)
	if !strings.Contains(string(b), `"joined_at": "2026-10-01T10:00:00Z"`) && !strings.Contains(string(b), `"joined_at":"2026-10-01T10:00:00Z"`) {
		t.Fatalf("node.json lost the pipeline's own fields: %s", b)
	}
	if st := s.homeDB.State(); st == nil || !st.Healing || healed.Load() == 0 {
		t.Fatalf("not marked healing: %+v", st)
	}
	select {
	case <-reloadCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the machine does not start again with its new access")
	}
	if _, ok, detail := s.command(t.Context(), "home.access", args); !ok || detail.(map[string]any)["changed"] != false {
		t.Fatalf("the same access again: %v %v", ok, detail)
	}
	// a database on another machine than the home: its host
	other, _ := json.Marshal(map[string]any{"role": "maktaba_node_1e943eda", "password": "p2", "db_host": "db.lan",
		"db_port": 5433, "db_name": "maktaba", "database_tls": "off"})
	if _, ok, _ := s.command(t.Context(), "home.access", other); !ok {
		t.Fatal("an access on another host")
	}
	if n := config.LocalNodeConfig(); !strings.Contains(n.DatabaseURL, "@db.lan:5433/") || n.DatabaseTLS != "off" || n.DatabaseCA != "" {
		t.Fatalf("node.json: %+v", n)
	}
	// its start again, taken here (the reload must not reach the tests after this one)
	select {
	case <-reloadCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the machine does not start again with its access on another host")
	}
	for _, bad := range []map[string]any{
		{"role": "x; DROP", "password": "p", "db_port": 5432},
		{"role": "maktaba_node_1", "password": "", "db_port": 5432},
		{"role": "maktaba_node_1", "password": "p", "db_port": 0},
		{"role": "maktaba_node_1", "password": "p", "db_port": 5432, "database_ca": "not a certificate"},
	} {
		raw, _ := json.Marshal(bad)
		if _, ok, _ := s.command(t.Context(), "home.access", raw); ok {
			t.Errorf("a bad access was taken: %v", bad)
		}
	}
}

// Why a login to the home's database failed, as the home and the pages say it.
func TestClassifyHomeDB(t *testing.T) {
	for msg, want := range map[string]string{
		`pq: password authentication failed for user "maktaba_node_1e943eda"`:                                                  "auth_failed",
		`pq: role "maktaba_node_1e943eda" does not exist`:                                                                      "auth_failed",
		`pq: role "maktaba_node_1e943eda" is not permitted to log in`:                                                          "auth_failed",
		`pq: no pg_hba.conf entry for host "192.168.50.20", user "maktaba_node_1e943eda", database "maktaba", SSL off`:         "no_hba",
		`pq: database "maktaba" does not exist`:                                                                                "no_database",
		`dial tcp 192.168.50.177:5432: connect: connection refused`:                                                            "refused",
		`dial tcp 192.168.50.177:5432: connectex: No connection could be made because the target machine actively refused it.`: "refused",
		`x509: certificate signed by unknown authority`:                                                                        "tls",
		`dial tcp 10.0.0.9:5432: i/o timeout`:                                                                                  "unreachable",
		`context deadline exceeded`:                                                                                            "unreachable",
		`something else entirely`:                                                                                              "other",
	} {
		if got := classifyHomeDB(errors.New(msg)); got != want {
			t.Errorf("%q: %s, want %s", msg, got, want)
		}
	}
}

// The watch says each change once (the machine agent sends its state again), counts the
// failures in a row (the home acts on the second: one try may meet a home in the middle
// of a switch), keeps a healing machine healing until a login works, and says when the
// database is back.
func TestHomeDBWatch(t *testing.T) {
	var fail error // the probe runs in check, on this goroutine
	changes := 0
	w := &homeDBWatch{probe: func(context.Context) error { return fail }, onChange: func() { changes++ }}
	ctx := context.Background()
	w.check(ctx)
	if st := w.State(); st == nil || !st.OK || changes != 1 {
		t.Fatalf("first check: %+v (%d)", st, changes)
	}
	w.check(ctx)
	if changes != 1 {
		t.Fatalf("no change, told again (%d)", changes)
	}
	fail = errors.New(`pq: password authentication failed for user "r"`)
	w.check(ctx)
	if st := w.State(); st.OK || st.Code != "auth_failed" || st.Healing || st.Fails != 1 || changes != 2 {
		t.Fatalf("refused: %+v (%d)", st, changes)
	}
	w.check(ctx) // the second in a row: the home acts on it
	if st := w.State(); st.Fails != 2 || changes != 3 {
		t.Fatalf("refused again: %+v (%d)", st, changes)
	}
	w.check(ctx)
	if st := w.State(); st.Fails != 3 || changes != 3 {
		t.Fatalf("a third: told again (%d) %+v", changes, st)
	}
	w.healing()
	w.check(ctx) // the new access not taken yet (the machine starts again with it)
	if st := w.State(); st.OK || !st.Healing {
		t.Fatalf("healing: %+v", st)
	}
	fail = nil
	w.check(ctx)
	if st := w.State(); !st.OK || st.Healing || st.Fails != 0 {
		t.Fatalf("back: %+v", st)
	}
}

// After a failure the watch tries again soon (homeDBRecheck), so a failure the home acts on
// is known within seconds, not two of homeDBEvery.
func TestHomeDBWatchRechecksSoon(t *testing.T) {
	tries := atomic.Int32{}
	w := &homeDBWatch{probe: func(context.Context) error { tries.Add(1); return errors.New("connection refused") },
		first: time.Millisecond, every: time.Hour, recheck: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := w.State(); st != nil && st.Fails >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no second try soon after a failure: %d tries", tries.Load())
}

// A home's watch waits for its machines to follow its database (their access re-made),
// with progress as they do, and stops waiting for each after machinesWait from the first
// time it said so: a machine that starts again (gone from the list a moment) or flaps does
// not wait anew.
func TestHomeWatchWaitsForItsMachines(t *testing.T) {
	now := time.Date(2026, 10, 24, 11, 18, 0, 0, time.UTC)
	h := &machineHealth{home: true, now: func() time.Time { return now }}
	if _, wait := h.machinesOff(nil); wait {
		t.Fatal("no machine off: no wait")
	}
	off := []selfMachineDB{{ID: "n1", Name: "studio-gpu", Code: "auth_failed"}}
	v, wait := h.machinesOff(off)
	if !wait || !v.Pending || !strings.Contains(v.Why, "studio-gpu (auth_failed)") {
		t.Fatalf("waiting: %+v %v", v, wait)
	}
	off[0].Healing = true
	if v2, _ := h.machinesOff(off); v2.Progress == v.Progress {
		t.Fatal("following its new access is progress")
	}
	// it starts again (a moment gone from the list), then says so again
	now = now.Add(4 * time.Minute)
	if _, wait := h.machinesOff(nil); wait {
		t.Fatal("none off: no wait")
	}
	now = now.Add(30 * time.Second)
	if _, wait := h.machinesOff(off); !wait {
		t.Fatal("back off within its wait: waits")
	}
	now = now.Add(machinesWait - 4*time.Minute - 30*time.Second)
	if _, wait := h.machinesOff(off); wait {
		t.Fatal("still waiting after machinesWait from the first time (a restart reset it)")
	}
	// another machine off later holds it for its own wait
	two := append(off, selfMachineDB{ID: "n2", Name: "attic", Code: "refused"})
	v, wait = h.machinesOff(two)
	if !wait || !strings.Contains(v.Why, "attic") || strings.Contains(v.Why, "studio-gpu") {
		t.Fatalf("the second machine: %+v %v", v, wait)
	}
}

// An access without a CA (or TLS off) keeps how the machine verifies the database (never a
// downgrade to an unverified connection); one from a home of an earlier term, or while
// this machine switches or follows a new home, is refused; a bad host or database name is
// refused.
func TestHomeAccessKeepsTheCAAndRefusesAStaleHome(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node.json")
	t.Setenv("MAKTABA_NODE_CONFIG", node)
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	ca := testCAPEM(t)
	body, _ := json.Marshal(map[string]any{"node_id": "n1", "name": "studio", "private_key": "k",
		"home":         map[string]any{"url": "http://192.168.50.177:8080", "term": 3},
		"database_url": "postgres://maktaba_node_1:old@192.168.50.177:5432/maktaba", "database_ca": ca})
	if err := os.WriteFile(node, body, 0o600); err != nil {
		t.Fatal(err)
	}
	old := homeAccessReloadAfter
	homeAccessReloadAfter = 0
	t.Cleanup(func() { homeAccessReloadAfter = old })
	s := newSwitching(config.Config{})
	push := func(m map[string]any) (bool, any) {
		raw, _ := json.Marshal(m)
		_, ok, detail := s.command(t.Context(), "home.access", raw)
		return ok, detail
	}
	if ok, detail := push(map[string]any{"role": "maktaba_node_1", "password": "p", "db_port": 54329, "term": 2}); ok {
		t.Fatalf("a home of an earlier term: taken (%v)", detail)
	}
	for _, bad := range []map[string]any{
		{"role": "maktaba_node_1", "password": "p", "db_port": 5432, "term": 3, "db_host": "db.lan/x?sslmode=disable"},
		{"role": "maktaba_node_1", "password": "p", "db_port": 5432, "term": 3, "db_name": "maktaba; DROP"},
	} {
		if ok, _ := push(bad); ok {
			t.Errorf("taken: %v", bad)
		}
	}
	if ok, detail := push(map[string]any{"role": "maktaba_node_1", "password": "p", "db_port": 54329, "term": 3}); !ok {
		t.Fatalf("the access: %v", detail)
	}
	select {
	case <-reloadCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no new start")
	}
	if n := config.LocalNodeConfig(); n.DatabaseCA != ca || n.DatabaseTLS != "" || !strings.Contains(n.DatabaseURL, ":54329/") {
		t.Fatalf("the pinned CA lost: %+v", n)
	}
	s.mu.Lock()
	s.followed = true
	s.mu.Unlock()
	if ok, detail := push(map[string]any{"role": "maktaba_node_1", "password": "q", "db_port": 5433, "term": 3}); ok {
		t.Fatalf("taken while this machine follows a new home: %v", detail)
	}
}

// A login's secrets never reach the log, the home or the pages; the watch says where it
// logs in, and forgets a check made before the machine agent connected again.
func TestHomeDBWatchTargetAndSecrets(t *testing.T) {
	for in, want := range map[string]string{
		`node config: the home's database url: parse "postgres://maktaba_node_1:s3cret@db lan:5432/x": invalid character`: `parse "postgres://…@db lan:5432/x": invalid character`,
		`dial: host=db user=maktaba_node_1 password=s3cret dbname=maktaba`:                                                `password=… dbname=maktaba`,
	} {
		if got := noCreds(in); strings.Contains(got, "s3cret") || !strings.Contains(got, want) {
			t.Errorf("%q -> %q", in, got)
		}
	}
	w := &homeDBWatch{probe: func(context.Context) error {
		return errors.New(`pq: password authentication failed for user "maktaba_node_1" (postgres://maktaba_node_1:s3cret@h:5432/m)`)
	}, target: func() string { return "192.168.50.177:54329/maktaba" }, homeHost: func() string { return "192.168.50.177" },
		kick: make(chan struct{}, 1)}
	w.check(context.Background())
	st := w.State()
	if st.Target != "192.168.50.177:54329/maktaba" || st.HomeHost != "192.168.50.177" || strings.Contains(st.Detail, "s3cret") ||
		st.Code != "auth_failed" {
		t.Fatalf("%+v", st)
	}
	w.reconnected()
	if w.State() != nil || len(w.kick) != 1 {
		t.Fatalf("the check before the connection kept: %+v", w.State())
	}
}

// A machine whose failure is confirmed and that got no re-made access asks its home for
// one, homeDBAskAfter after the confirmation; then after homeDBAskFirst, twice as long each
// time up to homeDBAskMax. It never asks while it follows a re-made access, nor unconnected
// (it asks at the next check). The home's answer is kept in its state (its pages show it)
// until it logs in again, which starts its requests afresh.
func TestHomeDBWatchAsksForItsAccess(t *testing.T) {
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	fail := errors.New("dial tcp 192.168.50.177:5432: connect: connection refused")
	connected := true
	var asked []machineagent.HomeDB
	changes := 0
	w := &homeDBWatch{probe: func(context.Context) error { return fail }, clock: func() time.Time { return now },
		target: func() string { return "192.168.50.177:5432/maktaba" }, onChange: func() { changes++ },
		ask: func(st machineagent.HomeDB) bool {
			if connected {
				asked = append(asked, st)
			}
			return connected
		}}
	ctx := context.Background()
	step := func(d time.Duration) {
		now = now.Add(d)
		w.check(ctx)
		w.maybeAsk()
	}
	step(0) // one failure: not confirmed
	step(10 * time.Second)
	if len(asked) != 0 {
		t.Fatal("asked at once")
	}
	step(time.Minute)
	if len(asked) != 0 {
		t.Fatal("asked before the home's own re-made access had its time")
	}
	step(time.Minute) // two minutes after the confirmation
	if len(asked) != 1 || asked[0].Code != "refused" || asked[0].Target != "192.168.50.177:5432/maktaba" ||
		!w.State().Asked.Equal(now) {
		t.Fatalf("first request: %+v, state %+v", asked, w.State())
	}
	step(30 * time.Second)
	if len(asked) != 1 {
		t.Fatal("asked again too soon")
	}
	connected = false
	step(30 * time.Second) // due, but not connected
	connected = true
	step(10 * time.Second)
	if len(asked) != 2 {
		t.Fatalf("second request: %d", len(asked))
	}
	step(time.Minute + 50*time.Second)
	if len(asked) != 2 {
		t.Fatal("the second wait is not twice the first")
	}
	step(10 * time.Second)
	if len(asked) != 3 {
		t.Fatalf("third request: %d", len(asked))
	}
	for asks, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute, 7: time.Hour, 40: time.Hour} {
		if got := askAgainAfter(asks); got != want {
			t.Errorf("askAgainAfter(%d) = %s, want %s", asks, got, want)
		}
	}
	// the home's answer: kept through the next checks, said again (update.state)
	before := changes
	w.answered(machineagent.AccessAnswer{Code: "cannot_alter_role", Reason: "this home's database user may not change this machine's role", At: now})
	step(10 * time.Second)
	if st := w.State(); st.Answer == nil || st.Answer.Code != "cannot_alter_role" || changes == before {
		t.Fatalf("the answer: %+v (%d changes)", st, changes-before)
	}
	// following a re-made access: no request
	w.healing()
	step(time.Hour)
	if len(asked) != 3 {
		t.Fatal("asked while it follows a re-made access")
	}
	// it logs in: requests and answer behind it, and a new failure waits its time again
	fail = nil
	step(time.Second)
	if st := w.State(); !st.OK || st.Answer != nil || !st.Asked.IsZero() {
		t.Fatalf("back: %+v", st)
	}
	fail = errors.New("connection refused")
	step(time.Second)
	step(time.Second)
	step(time.Minute)
	if len(asked) != 3 {
		t.Fatal("a new failure asked before its time")
	}
	step(time.Minute)
	if len(asked) != 4 {
		t.Fatalf("a new failure's request: %d", len(asked))
	}
}
