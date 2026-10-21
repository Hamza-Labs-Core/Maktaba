package switchover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// fakeHub is the cluster channel: every machine's agent connected, answering as told.
type fakeHub struct {
	mu      sync.Mutex
	calls   []string
	answers map[string]func(args json.RawMessage) (bool, any)
	// nodes are the machines whose agents MachineAgents lists
	nodes []string
}

func (h *fakeHub) MachineAgent(node string) string { return "s-" + node }
func (h *fakeHub) HomeProcesses() []string         { return []string{"home-worker"} }
func (h *fakeHub) MachineAgents() map[string]string {
	out := map[string]string{}
	for _, n := range h.nodes {
		out[n] = "s-" + n
	}
	return out
}
func (h *fakeHub) CommandWithin(_ context.Context, sid, name string, args json.RawMessage, _ string, _ time.Duration) (agent.CmdResult, error) {
	h.mu.Lock()
	h.calls = append(h.calls, name+"@"+sid)
	fn := h.answers[name]
	h.mu.Unlock()
	ok, detail := true, any(nil)
	if fn != nil {
		ok, detail = fn(args)
	}
	b, _ := json.Marshal(detail)
	return agent.CmdResult{OK: ok, Detail: b}, nil
}

func (h *fakeHub) called() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// fakeSupervisor is this home's supervisor: it records the steps and freezes nothing.
type fakeSupervisor struct {
	mu    sync.Mutex
	steps []string
	kit   string
	fail  string
	// standbyHash is the standby kit's hash it gathers (one it holds already is not sent)
	standbyHash string
}

func (s *fakeSupervisor) Call(_ context.Context, step string, req, out any) error {
	s.mu.Lock()
	if m, ok := req.(map[string]any); ok && step == "relay" {
		// the relay agent stopped (false) or started again (true)
		step = fmt.Sprintf("relay:%v", m["on"])
	}
	s.steps = append(s.steps, step)
	s.mu.Unlock()
	if step == s.fail {
		return errors.New("refused")
	}
	if step == "kit" && out != nil {
		b, _ := json.Marshal(map[string]any{"path": s.kit, "chunks": 1})
		return json.Unmarshal(b, out)
	}
	if step == "standby-kit" && out != nil {
		m, _ := req.(map[string]any)
		ans := map[string]any{"hash": s.standbyHash, "path": s.kit, "term": 2}
		if m["have"] == s.standbyHash {
			ans = map[string]any{"hash": s.standbyHash, "unchanged": true}
		}
		b, _ := json.Marshal(ans)
		return json.Unmarshal(b, out)
	}
	return nil
}

func (s *fakeSupervisor) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

func waitState(t *testing.T, o *Orchestrator, id string, want ...string) *Progress {
	t.Helper()
	end := time.Now().Add(30 * time.Second)
	for time.Now().Before(end) {
		if p := o.Progress(id); p != nil {
			for _, w := range want {
				if p.State == w {
					return p
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("switch %s never reached %v: %+v", id, want, o.Progress(id))
	return nil
}

func setup(t *testing.T) (*Orchestrator, *fakeHub, *fakeSupervisor, string, string) {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	b, c := uuid.NewString(), uuid.NewString()
	for _, n := range []struct{ id, name string }{{b, "b"}, {c, "c"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, approved_at,
		        approved_by, full_node, replica, replica_reported_at)
		    VALUES ($1, $2, 'approved', 'windows', 'amd64', 'cHVi', now(), 'test', true,
		        '{"state": "following", "local_schema": 145}'::jsonb, now())`, n.id, n.name); err != nil {
			t.Fatal(err)
		}
	}
	kit := filepath.Join(t.TempDir(), "kit.chunks")
	// one sealed chunk's record: index 0, last, 3 bytes
	if err := os.WriteFile(kit, []byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 3, 'a', 'b', 'c'}, 0o600); err != nil {
		t.Fatal(err)
	}
	// the new home, answering as the home once promoted
	newHome := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"role":"home"}`))
	}))
	t.Cleanup(newHome.Close)
	h := &fakeHub{answers: map[string]func(json.RawMessage) (bool, any){
		"switch.prepare": func(json.RawMessage) (bool, any) { return true, map[string]any{"marker": true} },
		"switch.promote": func(json.RawMessage) (bool, any) {
			return true, NewHome{APIURL: newHome.URL, DBHost: "b", DBPort: 54329, SystemID: "7"}
		},
		"home.moved": func(json.RawMessage) (bool, any) { return true, map[string]any{"copy_kept": true} },
	}}
	s := &fakeSupervisor{kit: kit}
	o := &Orchestrator{DB: db, Hub: h, Supervisor: s, HomeName: "a", SchemaRev: 145, JobsWait: time.Second}
	return o, h, s, b, c
}

func TestAPlannedSwitchRunsItsStepsInOrder(t *testing.T) {
	o, h, s, b, c := setup(t)
	ctx := context.Background()
	cs, err := o.Candidates(ctx)
	if err != nil || len(cs) != 2 || !cs[0].Ready || !cs[1].Ready {
		t.Fatalf("candidates: %+v %v", cs, err)
	}
	id, err := o.Start(ctx, b, "user:"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "handed_over", "failed")
	if p.State != "handed_over" || p.NewHome == nil || p.NewHome.DBHost != "b" {
		t.Fatalf("the switch: %+v", p)
	}
	// the cloud relay agent stops on this home before the new home is promoted
	if got := strings.Join(s.called(), " "); got != "check freeze kit relay:false demote" {
		t.Fatalf("the supervisor's steps: %v", got)
	}
	want := []string{"drain@home-worker", "switch.prepare@s-" + b, "switch.kit@s-" + b, "switch.promote@s-" + b,
		"home.moved@s-" + c}
	got := h.called()
	if len(got) != len(want) {
		t.Fatalf("the channel's commands: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the channel's commands: %v, want %v", got, want)
		}
	}
	if m := p.Machines[c]; m.State != "following" {
		t.Fatalf("the other machine: %+v", m)
	}
	// the machines' jobs held for the switch; the row is there for the new home
	var drained int
	_ = o.DB.QueryRowContext(ctx, `SELECT count(*) FROM cluster_nodes WHERE drain`).Scan(&drained)
	var state string
	_ = o.DB.QueryRowContext(ctx, `SELECT state FROM cluster_switchovers WHERE id = $1`, id).Scan(&state)
	if drained != 2 || state != "freezing" {
		t.Fatalf("drained %d, the row %q", drained, state)
	}
	// a second switch waits for this one... it is over here: a new one may start
	if cs, _ := o.Candidates(ctx); len(cs) == 0 {
		t.Fatal("no candidates after the hand-over")
	}
}

// A copy without the marker refuses the switch, and everything is as it was: the
// database thawed, the machines' jobs back, the row failed.
func TestASwitchWithoutTheMarkerIsUndone(t *testing.T) {
	o, h, s, b, _ := setup(t)
	ctx := context.Background()
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = true WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	h.answers["switch.prepare"] = func(json.RawMessage) (bool, any) { return true, map[string]any{"marker": false} }
	id, err := o.Start(ctx, b, "system")
	if err != nil {
		t.Fatal(err)
	}
	p := waitState(t, o, id, "failed", "handed_over")
	if p.State != "failed" {
		t.Fatalf("the switch: %+v", p)
	}
	// the database thawed, the relay agent (if it ran) going again
	if got := strings.Join(s.called(), " "); !strings.HasSuffix(got, "thaw relay:true") {
		t.Fatalf("the supervisor's steps: %v (the database left frozen)", got)
	}
	var drainB, drainOther bool
	_ = o.DB.QueryRowContext(ctx, `SELECT drain FROM cluster_nodes WHERE id = $1`, b).Scan(&drainB)
	_ = o.DB.QueryRowContext(ctx, `SELECT bool_or(drain) FROM cluster_nodes WHERE id <> $1`, b).Scan(&drainOther)
	if !drainB || drainOther {
		t.Fatalf("drains after the undo: %v (it was drained before) %v", drainB, drainOther)
	}
	var state string
	_ = o.DB.QueryRowContext(ctx, `SELECT state FROM cluster_switchovers WHERE id = $1`, id).Scan(&state)
	if state != "failed" {
		t.Fatalf("the row: %q", state)
	}
	calls := h.called()
	if calls[len(calls)-1] != "undrain@home-worker" {
		t.Fatalf("the home's jobs not given back: %v", calls)
	}
}

func TestAMachineThatCannotTakeOverIsRefused(t *testing.T) {
	o, _, _, b, c := setup(t)
	ctx := context.Background()
	if _, err := o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET replica = '{"state": "copying", "local_schema": 144}'::jsonb
	    WHERE id = $1`, c); err != nil {
		t.Fatal(err)
	}
	cs, _ := o.Candidates(ctx)
	for _, x := range cs {
		if x.NodeID == c && (x.Ready || len(x.Reasons) != 2) {
			t.Fatalf("a machine copying, at another schema: %+v", x)
		}
	}
	if _, err := o.Start(ctx, c, "system"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("started a switch to it: %v", err)
	}
	_ = b
}

// Plex's Live TV moves with the home: the plex_livetv stage is asked once (queued), the
// switch waits while it has not answered, then reports what it did. A tuner address set by
// hand at another machine needs a person.
func TestPlexLiveTVMovesWithTheHome(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	var pool string
	if err := db.QueryRowContext(ctx, `SELECT pool FROM worker_pools ORDER BY pool LIMIT 1`).Scan(&pool); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane) VALUES ('plex_livetv', 'Plex Live TV',
		    '{catalog}', '` + pool + `', 'channels') ON CONFLICT (stage) DO NOTHING`,
		`INSERT INTO plex_livetv_state (id, status, updated_at) VALUES (1,
		    '{"maktaba": {"registered": true}, "target": {"tuner_url": "http://old-home:8782"}}', now())`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	here := "http://new-home:8782"
	got := liveTV(ctx, db, func() string { return here })
	if got.State != Pending {
		t.Fatalf("asked: %+v", got)
	}
	var requested string
	var asked bool
	_ = db.QueryRowContext(ctx, `SELECT requested_base_url, register_requested_at IS NOT NULL FROM plex_livetv_state`).
		Scan(&requested, &asked)
	var queued int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs WHERE stage = 'plex_livetv'`).Scan(&queued)
	if requested != here || !asked || queued != 1 {
		t.Fatalf("the request: %q %v, %d jobs", requested, asked, queued)
	}
	// asked once: the next check waits for the stage
	if got := liveTV(ctx, db, func() string { return here }); got.State != Pending {
		t.Fatalf("waiting: %+v", got)
	}
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs WHERE stage = 'plex_livetv'`).Scan(&queued)
	if queued != 1 {
		t.Fatalf("asked again: %d jobs", queued)
	}
	// the stage moved the tuner and answered
	if _, err := db.ExecContext(ctx, `UPDATE plex_livetv_state SET register_requested_at = NULL,
	    status = '{"maktaba": {"registered": true}, "target": {"tuner_url": "http://new-home:8782"},
	               "actions": {"register": "moved", "move": "in_place"}}'`); err != nil {
		t.Fatal(err)
	}
	if got := liveTV(ctx, db, func() string { return here + "/" }); got.State != Moved {
		t.Fatalf("moved: %+v", got)
	}
	// it answered without moving it (the tuner off on this machine)
	if _, err := db.ExecContext(ctx, `UPDATE plex_livetv_state SET
	    status = '{"maktaba": {"registered": false}, "actions": {"register": "tuner_off"}}'`); err != nil {
		t.Fatal(err)
	}
	if got := liveTV(ctx, db, func() string { return here }); got.State != NeedsYou || got.Detail != "tuner_off" {
		t.Fatalf("not moved: %+v", got)
	}
	// a tuner address set by hand names the old home
	if _, err := db.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ('rules.plexlive',
	    '{"tuner_url": "http://old-home:8782"}') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	if got := liveTV(ctx, db, func() string { return here }); got.State != NeedsYou || got.Detail != "http://old-home:8782" {
		t.Fatalf("pinned: %+v", got)
	}
	// a home that never had Maktaba in Plex
	if _, err := db.ExecContext(ctx, `DELETE FROM plex_livetv_state`); err != nil {
		t.Fatal(err)
	}
	if got := liveTV(ctx, db, func() string { return here }); got.State != NotUsed {
		t.Fatalf("never used: %+v", got)
	}
}

func TestSameHost(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"http://192.168.1.20:8782", "http://192.168.1.20:8782/xmltv.xml", true},
		{"192.168.1.20:8782", "http://192.168.1.20:8782", true},
		{"http://Home.local", "http://home.local:80", true},
		{"https://home.local", "http://home.local", false},
		{"http://a:8782", "http://b:8782", false},
		{"", "", false},
	} {
		if got := sameHost(c.a, c.b); got != c.want {
			t.Errorf("sameHost(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// GET /api/cluster/moved/{node}: the latest switch's notice for that machine; 404 for a
// machine with none.
func TestTheMovedNoticeIsServedPerMachine(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	c := uuid.NewString()
	for term, notice := range map[int]string{2: "old", 3: "new"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name,
		        state, notices) VALUES ($1, $2, 'planned', $3, 'b', 'a', 'done', jsonb_build_object($4::text, $5::text))`,
			uuid.NewString(), term, uuid.NewString(), c, notice); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	r.Get("/api/cluster/moved/{node}", MovedHandler(db))
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/cluster/moved/" + c)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Term   int64  `json:"term"`
		Notice string `json:"notice"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || got.Term != 3 || got.Notice != "new" {
		t.Fatalf("the notice: %d %+v", resp.StatusCode, got)
	}
	for _, other := range []string{uuid.NewString(), "not-a-machine"} {
		resp, err := http.Get(srv.URL + "/api/cluster/moved/" + other)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d", other, resp.StatusCode)
		}
	}
	if n := TermOf(ctx, db); n != 1 {
		t.Fatalf("the term: %d", n)
	}
}
