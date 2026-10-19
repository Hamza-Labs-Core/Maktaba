package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

func TestNodeHandshakeWelcomeAndRegistry(t *testing.T) {
	f := newFixture(t)
	a, nodeID := f.node("studio")
	settings, _ := a.Welcome["settings"].(map[string]any)
	if settings["log_level"] != "info" || settings["metrics_every"] != float64(2) ||
		settings["retention_days"] != float64(14) || settings["file_mb"] != float64(256) {
		t.Fatalf("settings = %v", settings)
	}
	if _, has := settings["share_root"]; !has {
		t.Fatal("no share_root in settings")
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
	// the hub answers pings, echoing their ts, and keeps the metrics
	a.send(map[string]any{"t": "ping", "ts": "2026-10-19T00:00:00Z"})
	if pong := a.recvType("pong"); pong["ts"] != "2026-10-19T00:00:00Z" {
		t.Fatalf("pong = %v", pong)
	}
	a.send(map[string]any{"t": "metrics", "ts": "2026-10-19T00:00:00Z", "system": map[string]any{"cpu_pct": 12.5}})
	waitFor(t, "metrics", func() bool { return f.hub.Processes()[0].Metrics != nil })
	var m map[string]any
	_ = json.Unmarshal(f.hub.Processes()[0].Metrics, &m)
	if _, has := m["t"]; has || m["ts"] != "2026-10-19T00:00:00Z" {
		t.Fatalf("metrics = %v", m)
	}
	// unknown frame types are ignored (the counter still moves)
	a.send(map[string]any{"t": "something.new"})
	a.send(map[string]any{"t": "ping"})
	a.recvType("pong")
	// leaving unregisters, and the process is listed as gone
	_ = a.c.Close(websocket.StatusNormalClosure, "bye")
	waitFor(t, "unregister", func() bool { return len(f.hub.Processes()) == 0 })
	if g := f.hub.Gone(); len(g) != 1 || g[0].Session != a.Session || g[0].GoneAt == nil {
		t.Fatalf("gone = %+v", g)
	}
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
	if _, err := f.dialAgent(helloOpts{kind: "home", homeKey: make([]byte, 32)}); !isClose(err, CloseAuthFailed) {
		t.Fatalf("wrong home key: %v", err)
	}
	// a process may say which folder it writes (hello.ip)
	b, err := f.dialAgent(helloOpts{kind: "home", homeKey: f.homeKey, ip: "10.1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if f.hub.Session(b.Session).IP != "10.1.2.3" {
		t.Fatal("hello.ip ignored")
	}
}

func isClose(err error, code websocket.StatusCode) bool {
	c, _ := closeOf(err)
	return c == code
}

func TestRefusedWithoutKeys(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.HomeKey, c.ClusterKey = nil, nil })
	if _, err := f.dialAgent(helloOpts{kind: "home", homeKey: make([]byte, 32)}); !isClose(err, CloseAuthFailed) {
		t.Fatalf("home without key: %v", err)
	}
	id, priv := f.approvedNode("n")
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv}); !isClose(err, CloseAuthFailed) {
		t.Fatalf("node without cluster key: %v", err)
	}
}

func TestMachineDetectsAFakeHome(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("careful")
	// the machine holds another home's cluster key: the hub cannot prove
	// itself, so the keys differ and the hub refuses the proof
	_, otherPub, _ := cluster.GenerateKey()
	_, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, clusterPub: otherPub})
	if !isClose(err, CloseAuthFailed) {
		t.Fatalf("err = %v", err)
	}
	if len(f.hub.Processes()) != 0 {
		t.Fatal("registered")
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
		want websocket.StatusCode
	}{
		{"unknown node", helloOpts{kind: "node", nodeID: uuid.NewString(), nodePriv: priv}, CloseNotMember},
		{"bad proof", helloOpts{kind: "node", nodeID: id, nodePriv: priv, badProof: true}, CloseAuthFailed},
		{"bad kind", helloOpts{kind: "laptop", nodeID: id, nodePriv: priv}, CloseProtocolError},
		{"bad node id", helloOpts{kind: "node", nodeID: "not-a-uuid", nodePriv: priv}, CloseProtocolError},
		{"old version", helloOpts{kind: "node", nodeID: id, nodePriv: priv, version: 1}, CloseUnsupportedVersion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.dialAgent(c.o)
			if code, reason := closeOf(err); code != c.want {
				t.Fatalf("refusal = %d %q, want %d", code, reason, c.want)
			}
			m := b.next(func(m browserMsg) bool { return m.Type == "process" })
			var ev processEvent
			_ = json.Unmarshal(m.Data, &ev)
			if ev.Event != "refused" || ev.Reason == nil || ev.Session != nil {
				t.Fatalf("event = %s", m.Data)
			}
		})
	}
	if _, reason := closeOf(func() error {
		_, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, version: 9})
		return err
	}()); reason != "unsupported_version 2" {
		t.Fatalf("version reason = %q", reason)
	}

	// pending and revoked look like unknown
	pendID := uuid.NewString()
	_, pub, _ := generateKeyPair(t)
	exp := time.Now().Add(time.Hour)
	_ = f.store.PutPending(context.Background(), nodeRow(pendID, pub, exp))
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: pendID, nodePriv: priv}); !isClose(err, CloseNotMember) {
		t.Fatalf("pending: %v", err)
	}
	_ = f.store.Revoke(context.Background(), id, time.Now())
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv}); !isClose(err, CloseNotMember) {
		t.Fatalf("revoked: %v", err)
	}
	if n := len(f.hub.Processes()); n != 0 {
		t.Fatalf("processes = %d", n)
	}
}

func TestHandshakeTimeoutAndRateLimit(t *testing.T) {
	old := HandshakeTime
	HandshakeTime = 300 * time.Millisecond
	t.Cleanup(func() { HandshakeTime = old })
	f := newFixture(t, func(c *Config) { c.AgentRatePerMin = 3 })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, f.wsURL(AgentPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Read(ctx) // say nothing
	if code, reason := closeOf(err); code != CloseTimeout || reason != "timeout handshake" {
		t.Fatalf("timeout: %d %q", code, reason)
	}
	// three handshakes a minute here: the fourth attempt is turned away
	for i := 0; i < 2; i++ {
		if _, err := f.dialAgent(helloOpts{kind: "home", homeKey: f.homeKey}); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := f.dialAgent(helloOpts{kind: "home", homeKey: f.homeKey}); !isClose(err, CloseRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestCounterReplayGapTamperAndText(t *testing.T) {
	f := newFixture(t)
	t.Run("replay", func(t *testing.T) {
		a, _ := f.node("a")
		frame, _ := a.c2s.Seal([]byte(`{"t":"ping"}`))
		ctx := context.Background()
		_ = a.c.Write(ctx, websocket.MessageBinary, frame)
		a.recvType("pong")
		_ = a.c.Write(ctx, websocket.MessageBinary, frame) // the same counter again
		if code, reason := a.closed(); code != CloseSealError || reason != "seal_error" {
			t.Fatalf("close = %d %q", code, reason)
		}
	})
	t.Run("gap", func(t *testing.T) {
		a, _ := f.node("b")
		_, _ = a.c2s.Seal([]byte(`{"t":"ping"}`)) // counter 0 never sent
		a.send(map[string]any{"t": "ping"})       // counter 1
		if code, _ := a.closed(); code != CloseSealError {
			t.Fatalf("close = %d", code)
		}
	})
	t.Run("tamper", func(t *testing.T) {
		a, _ := f.node("c")
		frame, _ := a.c2s.Seal([]byte(`{"t":"ping"}`))
		frame[len(frame)-1] ^= 1
		_ = a.c.Write(context.Background(), websocket.MessageBinary, frame)
		if code, _ := a.closed(); code != CloseSealError {
			t.Fatalf("close = %d", code)
		}
	})
	t.Run("text after welcome", func(t *testing.T) {
		a, _ := f.node("d")
		_ = a.c.Write(context.Background(), websocket.MessageText, []byte(`{"t":"ping"}`))
		if code, _ := a.closed(); code != CloseProtocolError {
			t.Fatalf("close = %d", code)
		}
	})
	t.Run("not json", func(t *testing.T) {
		a, _ := f.node("e")
		a.sendRaw([]byte(`hello`))
		if code, _ := a.closed(); code != CloseProtocolError {
			t.Fatalf("close = %d", code)
		}
	})
	t.Run("too large", func(t *testing.T) {
		a, _ := f.node("g")
		a.sendRaw(append(append([]byte(`{"t":"logs","pad":"`), bytes.Repeat([]byte("x"), MaxFrame)...), []byte(`"}`)...))
		if code, _ := a.closed(); code != CloseTooLarge {
			t.Fatalf("close = %d", code)
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
	if ping := a.recvType("ping"); ping["ts"] == nil { // the hub pings, with its time
		t.Fatalf("ping = %v", ping)
	}
	// say nothing: the hub closes after the silence limit
	if code, reason := a.closed(); code != CloseTimeout || reason != "timeout silence" {
		t.Fatalf("close = %d %q", code, reason)
	}
}

func TestSameWorkerReplaced(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("twin")
	first, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, workerID: "node-1-42"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, workerID: "node-1-42"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := first.closed(); code != CloseReplaced {
		t.Fatalf("close = %d", code)
	}
	waitFor(t, "one left", func() bool { ps := f.hub.Processes(); return len(ps) == 1 && ps[0].Session == second.Session })
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
	if code, reason := a.closed(); code != CloseRevoked || reason != "revoked" {
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
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv}); !isClose(err, CloseNotMember) {
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

	// GET /api/cluster/processes
	var snap struct {
		Processes []Process `json:"processes"`
		Files     Location  `json:"files"`
	}
	decode(t, f.do("GET", "/api/cluster/processes", "", "admin"), &snap)
	if len(snap.Processes) != 1 || snap.Files.Dir != f.dir {
		t.Fatalf("processes = %+v", snap)
	}
	if r := f.do("GET", "/api/cluster/processes", "", "viewer"); r.StatusCode != 403 {
		t.Fatalf("viewer processes: %d", r.StatusCode)
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
	// disconnect closes the session (4014) even without an answer
	go func() {
		r := f.do("POST", "/api/cluster/processes/"+a.Session+"/cmd", `{"name":"disconnect"}`, "admin")
		var res CmdResult
		_ = json.NewDecoder(r.Body).Decode(&res)
		if r.StatusCode != 200 || !res.OK {
			t.Errorf("disconnect: %d %+v", r.StatusCode, res)
		}
	}()
	if code, _ := a.closed(); code != CloseDisconnect {
		t.Fatalf("close = %d", code)
	}
	waitFor(t, "session closed", func() bool { return len(f.hub.Processes()) == 0 })
}

func TestEventsFileRecordsConnections(t *testing.T) {
	f := newFixture(t)
	a, _ := f.node("logged")
	_ = a.c.Close(websocket.StatusNormalClosure, "")
	waitFor(t, "gone", func() bool { return len(f.hub.Processes()) == 0 })
	_, _ = f.dialAgent(helloOpts{kind: "node", nodeID: uuid.NewString(), nodePriv: make([]byte, 32)})
	waitFor(t, "events written", func() bool {
		f.hub.files.Flush()
		return countLines(t, eventsPath(f)) >= 3 // connect, disconnect, auth_refused
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
	want := map[string]bool{"connect": false, "disconnect": false, "auth_refused": false}
	for _, e := range events {
		want[e] = true
	}
	for e, ok := range want {
		if !ok {
			t.Fatalf("no %s event in %v", e, events)
		}
	}
}
