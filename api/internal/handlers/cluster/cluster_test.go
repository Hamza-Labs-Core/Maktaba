package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// The node's keys are the shared/testdata/cluster/vectors.json ones.
const (
	nodePrivB64 = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	nodePubB64  = "B6N8vBQgk8i3VdwbEOhstCY3StFqqFPtC9/AsrhtHHw="
	nodeID      = "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7"
)

// fakeRoles records role provisioning.
type fakeRoles struct {
	mu        sync.Mutex
	supported bool
	reason    string
	created   map[string]string // role -> password
	disabled  []string
	dropped   []string
	createErr error
}

func (f *fakeRoles) Supported(context.Context) (bool, string) { return f.supported, f.reason }
func (f *fakeRoles) DatabaseName(context.Context) (string, error) {
	return "maktaba", nil
}
func (f *fakeRoles) Create(_ context.Context, role, pw string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.created[role] = pw
	return nil
}
func (f *fakeRoles) Disable(_ context.Context, role string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disabled = append(f.disabled, role)
	return 1, nil
}
func (f *fakeRoles) Drop(_ context.Context, role string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, role)
	return nil
}

type fixture struct {
	t     *testing.T
	h     *Handler
	store *cluster.MemoryStore
	roles *fakeRoles
	pub   *discovery.NoopPublisher
	r     chi.Router
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:     t,
		store: cluster.NewMemoryStore(),
		roles: &fakeRoles{supported: true, created: map[string]string{}},
		pub:   &discovery.NoopPublisher{},
		r:     chi.NewRouter(),
		now:   t0,
	}
	f.store.KnownStages = map[string]bool{"probe": true, "transcribe": true}
	f.h = &Handler{
		Store:          f.store,
		Roles:          f.roles,
		Advertiser:     &cluster.Advertiser{Publisher: f.pub, Port: 8080, Version: "1.2.3", SchemaRev: 103},
		PGPort:         6543,
		JoinRatePerMin: -1,
		PollRatePerMin: -1,
		NowFunc:        func() time.Time { return f.now },
	}
	f.h.Mount(f.r)
	if _, err := f.h.Bootstrap(context.Background(), "home-mac"); err != nil {
		t.Fatal(err)
	}
	return f
}

func admin() *principal.Principal  { return &principal.Principal{UserID: "u1", IsAdmin: true} }
func viewer() *principal.Principal { return &principal.Principal{UserID: "u2"} }

func (f *fixture) do(method, path, body string, p *principal.Principal) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "192.168.1.20:8080"
	req.RemoteAddr = "192.168.1.50:51000"
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, want int, v any) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d (want %d): %s", rec.Code, want, rec.Body.String())
	}
	if v != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	var m map[string]any
	decode(t, rec, status, &m)
	if rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if code != "" && m["code"] != code {
		t.Errorf("code = %v, want %s (%v)", m["code"], code, m)
	}
	return m
}

func (f *fixture) openJoins() {
	f.t.Helper()
	decode(f.t, f.do("PUT", "/api/cluster/settings", `{"accept_joins":true}`, admin()), 200, nil)
}

func joinBody(id, pub string) string {
	b, _ := json.Marshal(map[string]any{
		"node_id": id, "name": "linux-box", "os": "linux", "arch": "x86_64",
		"versions": map[string]string{"pipeline": "1.2.3"}, "capabilities": map[string]any{"cpu_count": 8},
		"storage": map[string]any{"read": "ok"}, "pubkey": pub, "future_field": 1,
	})
	return string(b)
}

func (f *fixture) join() JoinResponse {
	f.t.Helper()
	var jr JoinResponse
	decode(f.t, f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), http.StatusAccepted, &jr)
	return jr
}

func (f *fixture) poll(pub string) *httptest.ResponseRecorder {
	return f.do("GET", "/api/cluster/join/"+nodeID+"?pubkey="+url.QueryEscape(pub), "", nil)
}

// nodeCode is the code the node shows: SAS from its own key and the home's.
func nodeCode(t *testing.T, homePubB64 string) string {
	t.Helper()
	priv, _ := cluster.DecodeKey(nodePrivB64)
	nodePub, _ := cluster.DecodeKey(nodePubB64)
	homePub, err := cluster.DecodeKey(homePubB64)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := cluster.SharedSecret(priv, homePub)
	code, _ := cluster.SAS(shared, nodePub, homePub)
	return code
}

func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func (f *fixture) approve(code string) *httptest.ResponseRecorder {
	return f.do("POST", "/api/cluster/nodes/"+nodeID+"/approve", `{"code":"`+code+`"}`, admin())
}

// ---------- join ----------

func TestJoinClosedByDefault(t *testing.T) {
	f := newFixture(t)
	problemOf(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), 403, "joins_closed")
	if txt := f.pub.Last().TXT; txt["join"] != "0" || txt["role"] != "home" || txt["protocol"] != "1" {
		t.Errorf("TXT = %v", txt)
	}
}

func TestJoinUnsupportedRoles(t *testing.T) {
	f := newFixture(t)
	f.roles.supported, f.roles.reason = false, cluster.ReasonCannotCreate
	problemOf(t, f.do("PUT", "/api/cluster/settings", `{"accept_joins":true}`, admin()), 409, "roles_unsupported")
	m := problemOf(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), 403, "joins_closed")
	if m["detail"] != cluster.ReasonCannotCreate {
		t.Errorf("detail = %v", m["detail"])
	}
	var ov Overview
	decode(t, f.do("GET", "/api/cluster", "", admin()), 200, &ov)
	if ov.Home.RolesSupported || ov.Home.RolesReason == nil || *ov.Home.RolesReason != cluster.ReasonCannotCreate {
		t.Errorf("home = %+v", ov.Home)
	}
}

func TestJoinOpen(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	if f.pub.Last().TXT["join"] != "1" {
		t.Errorf("TXT join = %v", f.pub.Last().TXT)
	}
	jr := f.join()
	id, _ := f.store.Identity(context.Background())
	if jr.RequestID != nodeID || jr.Home.InstanceID != id.InstanceID || jr.Home.Name != "home-mac" ||
		!jr.ExpiresAt.Equal(t0.Add(15*time.Minute)) {
		t.Errorf("join = %+v", jr)
	}
	if _, err := cluster.DecodeKey(jr.HomePubkey); err != nil {
		t.Errorf("home pubkey: %v", err)
	}
	n, _ := f.store.Node(context.Background(), nodeID)
	if n.State != "pending" || *n.RemoteAddr != "192.168.1.50" || *n.APIURL != "http://192.168.1.20:8080" || n.HomePrivkey == nil {
		t.Errorf("stored = %+v", n)
	}
	var pr PollResponse
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.State != "pending" || pr.Sealed != nil {
		t.Errorf("poll = %+v", pr)
	}

	// Validation.
	problemOf(t, f.do("POST", "/api/cluster/join", joinBody("nope", nodePubB64), nil), 400, "")
	problemOf(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, "AAAA"), nil), 400, "")
	problemOf(t, f.do("POST", "/api/cluster/join", `{"node_id":"`+nodeID+`","name":"x","pubkey":"`+nodePubB64+`","storage":[1]}`, nil), 400, "")
}

func TestJoinRateLimit(t *testing.T) {
	f := newFixture(t)
	f.h.JoinRatePerMin = 2
	f.r = chi.NewRouter()
	f.h.Mount(f.r)
	f.openJoins()
	for i := 0; i < 2; i++ {
		decode(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), 202, nil)
	}
	rec := f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("third join: %d %s", rec.Code, rec.Body.String())
	}
}

func TestJoinConflictWhenMember(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), 200, nil)
	problemOf(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), 409, "already_member")
}

func TestPendingRequestKeepsItsKey(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	first := f.join()
	// someone who saw the node id asks with another key: refused, the request stands
	_, otherPub, err := cluster.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	problemOf(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, cluster.EncodeKey(otherPub)), nil), 409, "pending_elsewhere")
	// the machine itself asking again (same key) renews it
	again := f.join()
	if again.HomePubkey == first.HomePubkey {
		t.Error("renewal should give a fresh home key")
	}
	decode(t, f.approve(nodeCode(t, again.HomePubkey)), 200, nil)
}

// ---------- poll ----------

func TestPollStates(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	f.join()
	// Wrong key, unknown id, malformed: all 404.
	_, otherPub, _ := cluster.GenerateKey()
	problemOf(t, f.poll(cluster.EncodeKey(otherPub)), 404, "")
	problemOf(t, f.do("GET", "/api/cluster/join/"+nodeID, "", nil), 404, "")
	problemOf(t, f.do("GET", "/api/cluster/join/00000000-0000-0000-0000-000000000001?pubkey="+url.QueryEscape(nodePubB64), "", nil), 404, "")
	// An unencoded '+' arrives as a space and still matches.
	var pr PollResponse
	decode(t, f.do("GET", "/api/cluster/join/"+nodeID+"?pubkey="+nodePubB64, "", nil), 200, &pr)

	f.now = t0.Add(16 * time.Minute)
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.State != "expired" {
		t.Errorf("state = %q", pr.State)
	}
	// An expired request can be asked again.
	f.openJoins()
	f.join()
	decode(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/reject", "", admin()), 200, nil)
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.State != "rejected" {
		t.Errorf("state = %q", pr.State)
	}
}

// ---------- approve ----------

func TestApproveWrongCodes(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	bad := wrongCode(nodeCode(t, jr.HomePubkey))
	for left := 4; left >= 1; left-- {
		m := problemOf(t, f.approve(bad), 400, "wrong_code")
		if m["attempts_left"] != float64(left) {
			t.Errorf("attempts_left = %v, want %d", m["attempts_left"], left)
		}
	}
	m := problemOf(t, f.approve(bad), 400, "wrong_code")
	if m["attempts_left"] != float64(0) || m["state"] != "rejected" {
		t.Errorf("fifth = %v", m)
	}
	// Even the right code is refused now.
	problemOf(t, f.approve(nodeCode(t, jr.HomePubkey)), 409, "not_pending")
	var pr PollResponse
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.State != "rejected" || len(f.roles.created) != 0 {
		t.Errorf("state = %q, roles = %v", pr.State, f.roles.created)
	}
}

func TestApproveExpired(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	f.now = t0.Add(15 * time.Minute)
	problemOf(t, f.approve(nodeCode(t, jr.HomePubkey)), 410, "expired")
}

func TestApproveSealsCredentials(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	code := nodeCode(t, jr.HomePubkey)
	var nv Node
	// The page may send the code as shown: "123 456".
	decode(t, f.approve(code[:3]+" "+code[3:]), 200, &nv)
	if nv.State != "approved" || nv.DBRole == nil || *nv.DBRole != "maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7" ||
		nv.ApprovedBy == nil || *nv.ApprovedBy != "user:u1" {
		t.Errorf("node = %+v", nv)
	}
	pw, ok := f.roles.created["maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7"]
	if !ok || len(pw) != 43 {
		t.Fatalf("roles = %v", f.roles.created)
	}
	stored, _ := f.store.Node(context.Background(), nodeID)
	if stored.HomePrivkey != nil {
		t.Error("home private key must be cleared at approval")
	}

	var pr PollResponse
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.State != "approved" || pr.Sealed == nil {
		t.Fatalf("poll = %+v", pr)
	}
	priv, _ := cluster.DecodeKey(nodePrivB64)
	nodePub, _ := cluster.DecodeKey(nodePubB64)
	homePub, _ := cluster.DecodeKey(jr.HomePubkey)
	shared, _ := cluster.SharedSecret(priv, homePub)
	key, _ := cluster.SealKey(shared, nodePub, homePub)
	plain, err := cluster.Open(key, nodeID, *pr.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	var p cluster.Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		t.Fatal(err)
	}
	id, _ := f.store.Identity(context.Background())
	wantURL := "postgres://maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7:" + pw + "@192.168.1.20:6543/maktaba?sslmode=prefer"
	if p.V != 1 || p.NodeID != nodeID || p.DatabaseURL != wantURL ||
		p.Home != (cluster.PayloadHome{APIURL: "http://192.168.1.20:8080", InstanceID: id.InstanceID, Name: "home-mac"}) {
		t.Errorf("payload = %+v", p)
	}

	// Once the node's worker registers, the envelope is gone.
	f.store.AddWorker(cluster.NodeWorker{ID: "node-6f1c2a3b-1", NodeID: nodeID, Host: "linux-box",
		Stages: []string{"probe"}, LastSeenAt: t0})
	pr = PollResponse{}
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.State != "approved" || pr.Sealed != nil {
		t.Errorf("after register = %+v", pr)
	}
}

func TestSealedClearedAfterTTL(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), 200, nil)
	var pr PollResponse
	f.now = t0.Add(14 * time.Minute)
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.Sealed == nil {
		t.Fatal("sealed should still be there")
	}
	f.now = t0.Add(16 * time.Minute)
	pr = PollResponse{}
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.Sealed != nil {
		t.Error("sealed should be cleared 15 min after approval")
	}
}

func TestApproveRoleFailure(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	f.roles.createErr = errors.New("permission denied")
	problemOf(t, f.approve(nodeCode(t, jr.HomePubkey)), 500, "")
	n, _ := f.store.Node(context.Background(), nodeID)
	if n.State != "pending" {
		t.Errorf("state = %s", n.State)
	}
}

// ---------- reject / revoke / remove ----------

func TestRejectRevokeRemove(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	problemOf(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/revoke", "", admin()), 409, "not_approved")
	problemOf(t, f.do("DELETE", "/api/cluster/nodes/"+nodeID, "", admin()), 409, "not_removable")
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), 200, nil)
	problemOf(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/reject", "", admin()), 409, "not_pending")

	var nv Node
	decode(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/revoke", "", admin()), 200, &nv)
	if nv.State != "revoked" || nv.RevokedAt == nil || len(f.roles.disabled) != 1 || f.roles.disabled[0] != "maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7" {
		t.Errorf("node = %+v, disabled = %v", nv, f.roles.disabled)
	}
	var pr PollResponse
	decode(t, f.poll(nodePubB64), 200, &pr)
	if pr.State != "revoked" || pr.Sealed != nil {
		t.Errorf("poll = %+v", pr)
	}
	problemOf(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), 409, "already_member")

	rec := f.do("DELETE", "/api/cluster/nodes/"+nodeID, "", admin())
	if rec.Code != http.StatusNoContent || len(f.roles.dropped) != 1 {
		t.Fatalf("delete: %d %s dropped=%v", rec.Code, rec.Body.String(), f.roles.dropped)
	}
	problemOf(t, f.do("DELETE", "/api/cluster/nodes/"+nodeID, "", admin()), 404, "")
	// Removed: it may ask again.
	f.join()
}

// ---------- settings ----------

func TestSettingsAutoExpiryAndRename(t *testing.T) {
	f := newFixture(t)
	var home Home
	decode(t, f.do("PUT", "/api/cluster/settings", `{"accept_joins":true,"name":"Studio"}`, admin()), 200, &home)
	if !home.AcceptJoins || home.AcceptJoinsUntil == nil || !home.AcceptJoinsUntil.Equal(t0.Add(time.Hour)) ||
		home.Name != "Studio" || !home.RolesSupported || home.RolesReason != nil {
		t.Errorf("home = %+v", home)
	}
	if last := f.pub.Last(); last.Instance != "Maktaba (Studio)" || last.TXT["join"] != "1" || last.Port != 8080 ||
		last.TXT["schema_rev"] != "103" || last.TXT["version"] != "1.2.3" || len(last.TXT["id"]) != 8 {
		t.Errorf("announce = %+v", last)
	}
	f.now = t0.Add(59 * time.Minute)
	f.join()

	f.now = t0.Add(61 * time.Minute)
	var ov Overview
	decode(t, f.do("GET", "/api/cluster", "", admin()), 200, &ov)
	if ov.Home.AcceptJoins || ov.Home.AcceptJoinsUntil != nil {
		t.Errorf("joins should have closed: %+v", ov.Home)
	}
	if f.pub.Last().TXT["join"] != "0" {
		t.Errorf("announce after expiry = %v", f.pub.Last().TXT)
	}
	problemOf(t, f.do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), 403, "joins_closed")

	decode(t, f.do("PUT", "/api/cluster/settings", `{"accept_joins":false}`, admin()), 200, &home)
	problemOf(t, f.do("PUT", "/api/cluster/settings", `{"name":"  "}`, admin()), 400, "")
	problemOf(t, f.do("PUT", "/api/cluster/settings", `{"bogus":1}`, admin()), 400, "")
}

func TestHousekeepingClosesJoins(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	f.now = t0.Add(2 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.h.Run(ctx, time.Millisecond); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for f.pub.Last().TXT["join"] != "0" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done
	if f.pub.Last().TXT["join"] != "0" {
		t.Error("the loop should close joins and re-announce")
	}
}

// ---------- node edits and the overview ----------

func TestUpdateNodeAndOverview(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), 200, nil)
	f.store.AddWorker(cluster.NodeWorker{ID: "node-6f1c2a3b-1", NodeID: nodeID, Host: "linux-box",
		Stages: []string{"probe"}, StagesOff: json.RawMessage(`{"transcribe":"unavailable"}`), LastSeenAt: t0.Add(-10 * time.Second)})

	var nv Node
	decode(t, f.do("PUT", "/api/cluster/nodes/"+nodeID,
		`{"name":"NAS box","drain":true,"path_map":{"/Volumes/nas":"/mnt/nas"},"stages":{"transcribe":false,"probe":true}}`, admin()), 200, &nv)
	if nv.Name != "NAS box" || !nv.Drain || nv.PathMapSource == nil || *nv.PathMapSource != "admin" ||
		string(nv.PathMap) != `{"/Volumes/nas":"/mnt/nas"}` || nv.StageOverrides["transcribe"] || !nv.StageOverrides["probe"] {
		t.Errorf("node = %+v", nv)
	}
	nv = Node{}
	decode(t, f.do("PUT", "/api/cluster/nodes/"+nodeID, `{"path_map":null,"stages":{"probe":null}}`, admin()), 200, &nv)
	if string(nv.PathMap) != "null" || nv.PathMapSource != nil || len(nv.StageOverrides) != 1 {
		t.Errorf("node = %+v", nv)
	}
	problemOf(t, f.do("PUT", "/api/cluster/nodes/"+nodeID, `{"stages":{"nope":true}}`, admin()), 400, "unknown_stage")
	problemOf(t, f.do("PUT", "/api/cluster/nodes/"+nodeID, `{"path_map":[1]}`, admin()), 400, "")
	problemOf(t, f.do("PUT", "/api/cluster/nodes/00000000-0000-0000-0000-000000000001", `{"drain":false}`, admin()), 404, "")

	rec := f.do("GET", "/api/cluster", "", admin())
	var raw struct {
		Home  map[string]any   `json:"home"`
		Nodes []map[string]any `json:"nodes"`
	}
	decode(t, rec, 200, &raw)
	for _, k := range []string{"instance_id", "name", "accept_joins", "accept_joins_until", "roles_supported", "roles_reason"} {
		if _, ok := raw.Home[k]; !ok {
			t.Errorf("home missing %q", k)
		}
	}
	if len(raw.Nodes) != 1 {
		t.Fatalf("nodes = %v", raw.Nodes)
	}
	n := raw.Nodes[0]
	for _, k := range []string{"id", "name", "state", "os", "arch", "versions", "capabilities", "storage", "path_map",
		"path_map_source", "drain", "expires_at", "created_at", "approved_at", "revoked_at", "last_seen_at", "online",
		"workers", "stage_overrides"} {
		if _, ok := n[k]; !ok {
			t.Errorf("node missing %q", k)
		}
	}
	for _, k := range []string{"node_pubkey", "home_pubkey", "home_privkey", "sealed", "api_url"} {
		if _, ok := n[k]; ok {
			t.Errorf("node leaks %q", k)
		}
	}
	if n["online"] != true || n["capabilities"].(map[string]any)["cpu_count"] != float64(8) {
		t.Errorf("node = %v", n)
	}
	w := n["workers"].([]any)[0].(map[string]any)
	if w["id"] != "node-6f1c2a3b-1" || w["host"] != "linux-box" || w["stages_off"].(map[string]any)["transcribe"] != "unavailable" {
		t.Errorf("worker = %v", w)
	}
	if strings.Contains(rec.Body.String(), f.roles.created["maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7"]) {
		t.Error("the role password must never be in the overview")
	}

	f.now = t0.Add(5 * time.Minute)
	decode(t, f.do("GET", "/api/cluster", "", admin()), 200, &raw)
	if raw.Nodes[0]["online"] != false {
		t.Errorf("stale worker should be offline: %v", raw.Nodes[0]["online"])
	}
}

func TestAdminOnly(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	f.join()
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/cluster", ""},
		{"PUT", "/api/cluster/settings", `{"accept_joins":true}`},
		{"POST", "/api/cluster/nodes/" + nodeID + "/approve", `{"code":"123456"}`},
		{"POST", "/api/cluster/nodes/" + nodeID + "/reject", ""},
		{"POST", "/api/cluster/nodes/" + nodeID + "/revoke", ""},
		{"PUT", "/api/cluster/nodes/" + nodeID, `{"drain":true}`},
		{"DELETE", "/api/cluster/nodes/" + nodeID, ""},
	} {
		for _, p := range []*principal.Principal{nil, viewer()} {
			if rec := f.do(c.method, c.path, c.body, p); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v: %d", c.method, c.path, p, rec.Code)
			}
		}
	}
	n, _ := f.store.Node(context.Background(), nodeID)
	if n.State != "pending" || n.CodeAttempts != 0 || n.Drain {
		t.Errorf("non-admin calls changed the node: %+v", n)
	}
}

func TestSQLiteHome(t *testing.T) {
	store := cluster.NewMemoryStore()
	h := &Handler{Store: store, Roles: cluster.Unsupported{Reason: cluster.ReasonSQLite}, JoinRatePerMin: -1, PollRatePerMin: -1}
	r := chi.NewRouter()
	h.Mount(r)
	if _, err := h.Bootstrap(context.Background(), "pi"); err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string, p *principal.Principal) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	var ov Overview
	decode(t, do("GET", "/api/cluster", "", admin()), 200, &ov)
	if ov.Home.RolesSupported || ov.Home.RolesReason == nil || *ov.Home.RolesReason != cluster.ReasonSQLite ||
		ov.Home.Name != "pi" || len(ov.Nodes) != 0 {
		t.Errorf("overview = %+v", ov)
	}
	problemOf(t, do("PUT", "/api/cluster/settings", `{"accept_joins":true}`, admin()), 409, "roles_unsupported")
	problemOf(t, do("POST", "/api/cluster/join", joinBody(nodeID, nodePubB64), nil), 403, "joins_closed")
}
