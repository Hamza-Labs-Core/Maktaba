package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func TestNodeHandshakeWelcomeAndRegistry(t *testing.T) {
	f := newFixture(t)
	a, nodeID := f.node("studio")
	settings, _ := a.Welcome["settings"].(map[string]any)
	if settings["log_level"] != "info" || settings["metrics_every"] != float64(2) {
		t.Fatalf("settings = %v", settings)
	}
	if !sessionRe.MatchString(a.Session) {
		t.Fatalf("session id %q", a.Session)
	}
	ps := f.hub.Processes()
	if len(ps) != 1 {
		t.Fatalf("processes = %d", len(ps))
	}
	p := ps[0]
	if p.Kind != "node" || p.NodeID == nil || *p.NodeID != nodeID || p.NodeName == nil || *p.NodeName != "studio" ||
		p.IP != "127.0.0.1" || p.Host != "test-host" || p.PID != 4242 || p.Session != a.Session {
		t.Fatalf("process = %+v", p)
	}
	// the hub answers pings and tracks metrics
	a.send(map[string]any{"t": "ping"})
	a.recvType("pong")
	a.send(map[string]any{"t": "metrics", "ts": "2026-10-19T00:00:00Z", "system": map[string]any{"cpu_pct": 12.5}})
	waitFor(t, "metrics", func() bool { return f.hub.Processes()[0].Metrics != nil })
	var m map[string]any
	_ = json.Unmarshal(f.hub.Processes()[0].Metrics, &m)
	if _, has := m["t"]; has || m["ts"] != "2026-10-19T00:00:00Z" {
		t.Fatalf("metrics = %v", m)
	}
	// leaving unregisters
	_ = a.c.Close(websocket.StatusNormalClosure, "bye")
	waitFor(t, "unregister", func() bool { return len(f.hub.Processes()) == 0 })
}

func TestHomeHandshakeUsesHomeAddress(t *testing.T) {
	f := newFixture(t)
	a, err := f.dialAgent(helloOpts{kind: "home", homeKey: f.homeKey})
	if err != nil {
		t.Fatal(err)
	}
	p := f.hub.Processes()[0]
	if p.Kind != "home" || p.NodeID != nil || p.IP != testHomeIP || p.Session != a.Session {
		t.Fatalf("process = %+v", p)
	}
	// the wrong home key is refused
	wrong := make([]byte, 32)
	if _, err := f.dialAgent(helloOpts{kind: "home", homeKey: wrong}); closeReason(err) != RefuseBadProof {
		t.Fatalf("wrong home key: %v", err)
	}
}

func TestHomeRefusedWithoutKey(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.HomeKey = nil })
	if _, err := f.dialAgent(helloOpts{kind: "home", homeKey: make([]byte, 32)}); closeReason(err) != RefuseNoHomeKey {
		t.Fatalf("err = %v", err)
	}
}

func TestHandshakeRefusals(t *testing.T) {
	f := newFixture(t)
	b := f.browser("admin")
	b.subscribe(TopicProcesses)

	id, priv := f.approvedNode("ok")
	cases := []struct {
		name string
		o    helloOpts
		want string
	}{
		{"unknown node", helloOpts{kind: "node", nodeID: uuid.NewString(), nodePriv: priv}, RefuseUnknownNode},
		{"bad proof", helloOpts{kind: "node", nodeID: id, nodePriv: priv, badProof: true}, RefuseBadProof},
		{"bad kind", helloOpts{kind: "laptop", nodeID: id, nodePriv: priv}, RefuseBadHello},
		{"bad node id", helloOpts{kind: "node", nodeID: "not-a-uuid", nodePriv: priv}, RefuseBadHello},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.dialAgent(c.o)
			if got := closeReason(err); got != c.want {
				t.Fatalf("refusal = %q, want %q", got, c.want)
			}
			m := b.next(func(m browserMsg) bool { return m.Type == "process" })
			var ev processEvent
			_ = json.Unmarshal(m.Data, &ev)
			if ev.Event != "refused" || ev.Reason == nil || *ev.Reason != c.want || ev.Session != nil {
				t.Fatalf("event = %s", m.Data)
			}
		})
	}

	// a pending node is refused; so is a revoked one
	pendID := uuid.NewString()
	_, pub, _ := generateKeyPair(t)
	exp := time.Now().Add(time.Hour)
	_ = f.store.PutPending(context.Background(), nodeRow(pendID, pub, exp))
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: pendID, nodePriv: priv}); closeReason(err) != RefuseNotApproved {
		t.Fatalf("pending: %v", err)
	}
	_ = f.store.Revoke(context.Background(), id, time.Now())
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv}); closeReason(err) != RefuseRevoked {
		t.Fatalf("revoked: %v", err)
	}
	if n := len(f.hub.Processes()); n != 0 {
		t.Fatalf("processes = %d", n)
	}
}

func TestHandshakeVersionAndTimeout(t *testing.T) {
	old := HandshakeTime
	HandshakeTime = 300 * time.Millisecond
	t.Cleanup(func() { HandshakeTime = old })
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, f.wsURL(AgentPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = writeText(ctx, c, map[string]any{"t": "hello", "v": 2, "kind": "home", "nonce": "AAAAAAAAAAAAAAAAAAAAAA=="})
	_, _, err = c.Read(ctx)
	if closeReason(err) != RefuseVersion {
		t.Fatalf("version: %v", err)
	}

	c2, _, err := websocket.Dial(ctx, f.wsURL(AgentPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c2.Read(ctx) // say nothing
	if closeReason(err) != RefuseTimeout {
		t.Fatalf("timeout: %v", err)
	}
}

func TestCounterReplayGapAndTamperClose(t *testing.T) {
	f := newFixture(t)
	t.Run("replay", func(t *testing.T) {
		a, _ := f.node("a")
		frame, _ := a.c2s.Seal([]byte(`{"t":"ping"}`))
		ctx := context.Background()
		_ = a.c.Write(ctx, websocket.MessageBinary, frame)
		a.recvType("pong")
		_ = a.c.Write(ctx, websocket.MessageBinary, frame) // the same counter again
		if code, reason := a.closed(); code != websocket.StatusPolicyViolation || reason != "bad frame" {
			t.Fatalf("close = %d %q", code, reason)
		}
	})
	t.Run("gap", func(t *testing.T) {
		a, _ := f.node("b")
		_, _ = a.c2s.Seal([]byte(`{"t":"ping"}`)) // counter 0 never sent
		a.send(map[string]any{"t": "ping"})       // counter 1
		if code, _ := a.closed(); code != websocket.StatusPolicyViolation {
			t.Fatalf("close = %d", code)
		}
	})
	t.Run("tamper", func(t *testing.T) {
		a, _ := f.node("c")
		frame, _ := a.c2s.Seal([]byte(`{"t":"ping"}`))
		frame[len(frame)-1] ^= 1
		_ = a.c.Write(context.Background(), websocket.MessageBinary, frame)
		if code, _ := a.closed(); code != websocket.StatusPolicyViolation {
			t.Fatalf("close = %d", code)
		}
	})
	t.Run("unsealed", func(t *testing.T) {
		a, _ := f.node("d")
		_ = a.c.Write(context.Background(), websocket.MessageText, []byte(`{"t":"ping"}`))
		if code, reason := a.closed(); code != websocket.StatusPolicyViolation || reason != "unsealed frame" {
			t.Fatalf("close = %d %q", code, reason)
		}
	})
	waitFor(t, "all gone", func() bool { return len(f.hub.Processes()) == 0 })
}

func TestPingAndSilence(t *testing.T) {
	oldP, oldS := PingEvery, SilenceLimit
	PingEvery, SilenceLimit = 50*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { PingEvery, SilenceLimit = oldP, oldS })
	f := newFixture(t)
	a, _ := f.node("quiet")
	a.recvType("ping") // the hub pings
	// say nothing: the hub closes after the silence limit
	if code, reason := a.closed(); code != websocket.StatusPolicyViolation || reason != "timeout" {
		t.Fatalf("close = %d %q", code, reason)
	}
}

func TestRevokeClosesLiveSessionAndRefusesNew(t *testing.T) {
	f := newFixture(t)
	a, nodeID := f.node("doomed")
	other, _ := f.node("other")

	f.hub.RevokeNode(nodeID)
	cmd := a.recvType("cmd")
	if cmd["name"] != "disconnect" || cmd["args"].(map[string]any)["reason"] != "revoked" {
		t.Fatalf("cmd = %v", cmd)
	}
	if code, reason := a.closed(); code != websocket.StatusPolicyViolation || reason != "revoked" {
		t.Fatalf("close = %d %q", code, reason)
	}
	waitFor(t, "revoked session gone", func() bool { return len(f.hub.Processes()) == 1 })
	if f.hub.Processes()[0].Session != other.Session {
		t.Fatal("the other machine's session was closed")
	}
}

func TestRevokeRaceRefusesHandshakeThatReadTheRegistryBefore(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("racer")
	// a revocation newer than the handshake's registry read wins
	f.hub.mu.Lock()
	f.hub.revokedAt[id] = time.Now().Add(time.Hour)
	f.hub.mu.Unlock()
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv}); closeReason(err) != RefuseRevoked {
		t.Fatalf("err = %v", err)
	}
	// an older one (the machine was removed and approved again) does not
	f.hub.mu.Lock()
	f.hub.revokedAt[id] = time.Now().Add(-time.Hour)
	f.hub.mu.Unlock()
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv}); err != nil {
		t.Fatalf("re-approved machine refused: %v", err)
	}
}

func TestNodeChangedSendsReloadStages(t *testing.T) {
	f := newFixture(t)
	a, nodeID := f.node("switchy")
	f.hub.NodeChanged(nodeID)
	cmd := a.recvType("cmd")
	if cmd["name"] != "reload_stages" {
		t.Fatalf("cmd = %v", cmd)
	}
}

func TestCommandRoundTripREST(t *testing.T) {
	f := newFixture(t)
	a, _ := f.node("cmdr")
	done := make(chan *http.Response, 1)
	go func() {
		done <- f.do("POST", "/api/cluster/processes/"+a.Session+"/cmd", `{"name":"log_level","args":{"level":"WARN"}}`, "admin")
	}()
	cmd := a.recvType("cmd")
	if cmd["name"] != "log_level" || cmd["args"].(map[string]any)["level"] != "warning" {
		t.Fatalf("cmd = %v", cmd)
	}
	a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true, "detail": "level warning"})
	resp := <-done
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var res CmdResult
	decode(t, resp, &res)
	if !res.OK || string(res.Detail) != `"level warning"` || res.ID != cmd["id"] {
		t.Fatalf("result = %+v", res)
	}
	if f.hub.Processes()[0].LogLevel != "warning" {
		t.Fatal("log level not recorded")
	}

	// errors
	if r := f.do("POST", "/api/cluster/processes/"+a.Session+"/cmd", `{"name":"format_disk"}`, "admin"); r.StatusCode != 400 {
		t.Fatalf("bad command: %d", r.StatusCode)
	}
	if r := f.do("POST", "/api/cluster/processes/"+a.Session+"/cmd", `{"name":"log_level","args":{"level":"loud"}}`, "admin"); r.StatusCode != 400 {
		t.Fatalf("bad level: %d", r.StatusCode)
	}
	if r := f.do("POST", "/api/cluster/processes/0123456789abcdef/cmd", `{"name":"drain"}`, "admin"); r.StatusCode != 404 {
		t.Fatalf("no session: %d", r.StatusCode)
	}
	if r := f.do("POST", "/api/cluster/processes/../cmd", `{"name":"drain"}`, "admin"); r.StatusCode != 404 {
		t.Fatalf("bad session: %d", r.StatusCode)
	}
	if r := f.do("POST", "/api/cluster/processes/"+a.Session+"/cmd", `{"name":"drain"}`, "viewer"); r.StatusCode != 403 {
		t.Fatalf("viewer: %d", r.StatusCode)
	}
}

func TestCommandTimeoutAndDisconnect(t *testing.T) {
	old := CmdTimeout
	CmdTimeout = 200 * time.Millisecond
	t.Cleanup(func() { CmdTimeout = old })
	f := newFixture(t)
	a, _ := f.node("mute")
	if r := f.do("POST", "/api/cluster/processes/"+a.Session+"/cmd", `{"name":"drain"}`, "admin"); r.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("timeout: %d", r.StatusCode)
	}
	// disconnect closes the session even without an answer
	r := f.do("POST", "/api/cluster/processes/"+a.Session+"/cmd", `{"name":"disconnect"}`, "admin")
	var res CmdResult
	decode(t, r, &res)
	if r.StatusCode != 200 || !res.OK {
		t.Fatalf("disconnect: %d %+v", r.StatusCode, res)
	}
	waitFor(t, "session closed", func() bool { return len(f.hub.Processes()) == 0 })
}

func TestEventsFileRecordsConnections(t *testing.T) {
	f := newFixture(t)
	a, _ := f.node("logged")
	_ = a.c.Close(websocket.StatusNormalClosure, "")
	waitFor(t, "gone", func() bool { return len(f.hub.Processes()) == 0 })
	_, _ = f.dialAgent(helloOpts{kind: "node", nodeID: uuid.NewString(), nodePriv: make([]byte, 32)})
	f.hub.files.Flush()
	waitFor(t, "events written", func() bool {
		f.hub.files.Flush()
		return countLines(t, eventsPath(f)) >= 4 // files_dir, connected, disconnected, refused
	})
	var events []string
	for _, l := range readLines(t, eventsPath(f)) {
		var m map[string]any
		_ = json.Unmarshal([]byte(l), &m)
		events = append(events, m["event"].(string))
		if m["ts"] == nil {
			t.Fatalf("no ts: %s", l)
		}
	}
	want := map[string]bool{"files_dir": false, "connected": false, "disconnected": false, "refused": false}
	for _, e := range events {
		want[e] = true
	}
	for e, ok := range want {
		if !ok {
			t.Fatalf("no %s event in %v", e, events)
		}
	}
}
