//go:build cloudrehearsal

package rehearsal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	edgeh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/edge"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/plans"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
)

// rehearsalEdgeKey is the rehearsal's edge key: a fixed seed whose public half is in
// tools/rehearsal-cloud/broadcast-plans.toml. Rehearsal only, never a real key.
func rehearsalEdgeKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x50}, 32))
}

// fakeEdge plays the broadcast edge (story 50.21's PublisherControl) as story 50.20 §2–§3 asks
// of it: a new session gets the publisher's mode for new sessions (wait and off refuse it), a
// playing session keeps the mode it started in, off stops every playing session, each playing
// session's minutes go into the current window by its mode, and windows the API can't take stay
// queued and go later under the same keys.
type fakeEdge struct {
	t         *testing.T
	api       string
	client    *http.Client
	priv      ed25519.PrivateKey
	publisher string
	state     billing.PublisherState
	sessions  map[string]string // playing session -> its mode
	stopped   map[string]string // session -> why it stopped
	queue     []billing.UsageWindow
	window    time.Time
}

// start is a new viewer: true when it plays, with its mode.
func (e *fakeEdge) start(id string) (string, bool) {
	switch e.state.Mode {
	case billing.BroadcastFull, billing.BroadcastLowest:
		if len(e.sessions) >= e.state.MaxConcurrentViewers {
			return billing.BroadcastWait, false
		}
		e.sessions[id] = e.state.Mode
		return e.state.Mode, true
	}
	return e.state.Mode, false
}

// heartbeat is a minute of every playing session, counted in the current window by its mode.
func (e *fakeEdge) heartbeat() {
	if len(e.queue) == 0 || !e.queue[len(e.queue)-1].Window.Equal(e.window) {
		e.queue = append(e.queue, billing.UsageWindow{Publisher: e.publisher, Window: e.window})
	}
	w := &e.queue[len(e.queue)-1]
	for _, mode := range e.sessions {
		if mode == billing.BroadcastLowest {
			w.MinutesLowest++
		} else {
			w.MinutesFull++
		}
	}
}

// next moves to the next 5-minute window.
func (e *fakeEdge) next() { e.window = e.window.Add(billing.WindowLength) }

// bulk queues a window standing for many viewers at once (to reach the month's thresholds).
func (e *fakeEdge) bulk(full, lowest int64) {
	e.queue = append(e.queue, billing.UsageWindow{Publisher: e.publisher, Window: e.window, MinutesFull: full, MinutesLowest: lowest})
	e.next()
}

// flush posts the queue; the API down leaves it queued. It applies the state the API answers:
// off stops every playing session; any other mode applies to new sessions only.
func (e *fakeEdge) flush() (taken, dups int, ok bool) {
	body, _ := json.Marshal(edgeh.UsagePost{Edge: "rehearsal-edge", Windows: e.queue})
	req, _ := http.NewRequest(http.MethodPost, e.api+"/_edge/usage", bytes.NewReader(body))
	edgeh.Sign(e.priv, req, body, time.Now())
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, false
	}
	var out edgeh.UsageReply
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		e.t.Fatalf("usage reply: %v", err)
	}
	if len(out.Rejected) > 0 {
		e.t.Errorf("windows rejected: %+v", out.Rejected)
	}
	e.queue = nil
	for _, st := range out.States {
		if st.Publisher == e.publisher {
			e.apply(st)
		}
	}
	return len(out.Taken), len(out.Duplicates), true
}

func (e *fakeEdge) apply(st billing.PublisherState) {
	e.state = st
	if st.Mode == billing.BroadcastOff {
		for id := range e.sessions {
			e.stopped[id] = "off"
			delete(e.sessions, id)
		}
	}
}

// pull is the edge's 5-minute pull of the publisher's state.
func (e *fakeEdge) pull() {
	req, _ := http.NewRequest(http.MethodGet, e.api+"/_edge/publishers/"+e.publisher, nil)
	edgeh.Sign(e.priv, req, nil, time.Now())
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("pull: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var st billing.PublisherState
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&st) != nil {
		e.t.Fatalf("pull: %d", resp.StatusCode)
	}
	e.apply(st)
}

// C18 broadcast-plans (story 50.20 T10): a Broadcaster publisher driven across every mode by the
// edge's usage posts to this commit's api role (the plans build, broadcasting on and enforced).
// The modes come in order (full, lowest at the allowance, wait at twice it, off when the account
// is suspended); a new session gets the mode of its moment, nobody playing is stopped except on
// off; a 2-hour API outage queues the windows and the month's total is exact after; a window
// posted twice counts once. The edge is the scenario's own (fakeEdge); story 50.21's Worker in
// Miniflare takes its place when it lands (50.29).
func broadcastPlans(t *testing.T, c *cluster) Result {
	ctx := context.Background()
	plans.Apply()
	start := time.Now()
	var notes []string
	var requests, failed int64
	check := func(what string, ok bool, detail ...any) {
		t.Helper()
		requests++
		if !ok {
			failed++
			t.Errorf("%s: %v", what, detail)
		}
	}

	// a Broadcaster publisher: a Pro home with the add-on
	owner, ids, err := seed.HomesOf(ctx, c.db, "publisher@example.test", 0, 1, billing.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, slug := simhome.Creds(0)
	id := ids[slug]
	if _, err := c.db.ExecContext(ctx, `UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	// the api role (the plans build), its logs kept beside the scenario's
	work := filepath.Join(c.dir, ".work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "symbols.token"), []byte("rehearsal-unused\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		logs, _ := c.compose("--profile", "api", "logs", "--no-color", "--timestamps", "api")
		_ = os.WriteFile(filepath.Join(outDir(t), "broadcast-plans-api.log"), []byte(logs), 0o644)
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = c.compose("--profile", "api", "rm", "-fsv", "api")
		}
	})
	c.must("--profile", "api", "up", "-d", "--wait", "api")
	api := fmt.Sprintf("http://127.0.0.1:%d", portsFor(c.project)[1]+503)
	client := &http.Client{Timeout: 30 * time.Second, Transport: hostTransport{"api.test"}}
	waitFor(t, time.Minute, "the api role", func() bool {
		resp, err := client.Get(api + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	// the home publishes: refused until its owner accepts the agreement
	publish := func(slug string) (int, string) {
		body := strings.NewReader(`{"slug":"` + slug + `","access":"open","on_demand":false,"rungs":3,"top_kbps":8000}`)
		req, _ := http.NewRequest(http.MethodPost, api+"/v1/broadcast/channels", body)
		req.SetBasicAuth(id, secret)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e.Error
	}
	code, why := publish("news")
	check("publish before the agreement", code == http.StatusConflict && why == "broadcast_agreement_required", code, why)
	if _, err := c.db.ExecContext(ctx, `INSERT INTO broadcast_agreements (account_id, version) VALUES ($1, $2)`,
		owner, billing.BroadcastAgreementVersion); err != nil {
		t.Fatal(err)
	}
	code, why = publish("news")
	check("publish after the agreement", code == http.StatusCreated, code, why)

	edge := &fakeEdge{t: t, api: api, client: client, priv: rehearsalEdgeKey(), publisher: id,
		sessions: map[string]string{}, stopped: map[string]string{},
		window: time.Now().UTC().Truncate(billing.WindowLength).Add(-12 * time.Hour)}
	edge.pull()
	check("a fresh month is full", edge.state.Mode == billing.BroadcastFull && edge.state.Plan == "broadcaster", edge.state)

	// full: two viewers play, their minutes counted
	for _, s := range []string{"a", "b"} {
		m, ok := edge.start(s)
		check("viewer "+s+" in full", ok && m == billing.BroadcastFull, m)
	}
	for range 5 {
		edge.heartbeat()
	}
	edge.next()
	// many viewers bring the month to the allowance (5,000 h): new sessions get the lowest rung
	edge.bulk(100_000, 0)
	edge.bulk(100_000, 0)
	edge.bulk(100_000-10, 0)
	if _, _, ok := edge.flush(); !ok {
		t.Fatal("the usage post failed")
	}
	check("at the allowance: lowest", edge.state.Mode == billing.BroadcastLowest, edge.state.Mode, edge.state.Usage)
	m, ok := edge.start("c")
	check("a new viewer at the allowance", ok && m == billing.BroadcastLowest, m)
	check("the viewers playing keep full", edge.sessions["a"] == billing.BroadcastFull && edge.sessions["b"] == billing.BroadcastFull, edge.sessions)

	// at twice the allowance: new viewers wait, nobody playing stops
	edge.bulk(0, 100_000)
	edge.bulk(0, 100_000)
	edge.bulk(0, 100_000)
	edge.flush()
	check("at 2x: wait", edge.state.Mode == billing.BroadcastWait, edge.state.Mode, edge.state.Usage)
	m, ok = edge.start("d")
	check("a new viewer at 2x waits", !ok && m == billing.BroadcastWait, m)
	check("nobody playing stopped", len(edge.sessions) == 3 && len(edge.stopped) == 0, edge.sessions, edge.stopped)
	before := edge.state.Usage

	// a 2-hour API outage: the edge keeps its windows and posts them when it returns
	c.must("--profile", "api", "stop", "api")
	for range 24 {
		for range 5 {
			edge.heartbeat()
		}
		edge.next()
	}
	if _, _, ok := edge.flush(); ok {
		t.Fatal("a usage post went through while the API was stopped")
	}
	queued := len(edge.queue)
	last := edge.queue[len(edge.queue)-1]
	c.must("--profile", "api", "start", "api")
	waitFor(t, time.Minute, "the api role back", func() bool {
		resp, err := client.Get(api + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	taken, _, ok := edge.flush()
	check("the outage's windows taken", ok && taken == queued && queued == 24, taken, queued)
	after := edge.state.Usage
	// sessions a and b in full, c in lowest: 5 minutes each per window, 24 windows
	check("the month's total exact", after.MinutesFull-before.MinutesFull == 2*5*24 && after.MinutesLowest-before.MinutesLowest == 5*24,
		before, after)
	// the edge retries a window: counted once
	edge.queue = []billing.UsageWindow{last}
	taken, dups, _ := edge.flush()
	check("a window posted twice counts once", taken == 0 && dups == 1 && edge.state.Usage == after, taken, dups, edge.state.Usage)
	check("still nobody stopped", len(edge.stopped) == 0, edge.stopped)
	notes = append(notes, fmt.Sprintf("modes full → lowest → wait; 24 windows of a 2 h outage taken, %d full + %d lowest minutes; a repeat counted once",
		after.MinutesFull-before.MinutesFull, after.MinutesLowest-before.MinutesLowest))

	// off: the account suspended; playing sessions stop at their next playlist
	if _, err := c.db.ExecContext(ctx, `UPDATE users SET status = 'suspended' WHERE id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	edge.pull()
	check("suspended: off", edge.state.Mode == billing.BroadcastOff, edge.state.Mode)
	check("off stops every playing session", len(edge.sessions) == 0 && len(edge.stopped) == 3, edge.sessions, edge.stopped)
	notes = append(notes, fmt.Sprintf("off stopped %d sessions; done in %s", len(edge.stopped), time.Since(start).Round(time.Second)))
	return Result{Requests: requests, FailedRequests: failed, Notes: notes}
}
