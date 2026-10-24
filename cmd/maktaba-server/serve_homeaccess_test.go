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

// The watch says each change once (the machine agent sends its state again), keeps a
// healing machine healing until a login works, and says when the database is back.
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
	if st := w.State(); st.OK || st.Code != "auth_failed" || st.Healing || changes != 2 {
		t.Fatalf("refused: %+v (%d)", st, changes)
	}
	w.healing()
	w.check(ctx) // the new access not taken yet (the machine starts again with it)
	if st := w.State(); st.OK || !st.Healing {
		t.Fatalf("healing: %+v", st)
	}
	fail = nil
	w.check(ctx)
	if st := w.State(); !st.OK || st.Healing {
		t.Fatalf("back: %+v", st)
	}
}

// A home's watch waits for its machines to follow its database (their access re-made),
// with progress as they do, and stops waiting after machinesWait.
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
	now = now.Add(machinesWait)
	if _, wait := h.machinesOff(off); wait {
		t.Fatal("still waiting after machinesWait")
	}
	if _, wait := h.machinesOff(nil); wait || !h.offSince.IsZero() {
		t.Fatal("all back: the wait starts afresh")
	}
}
