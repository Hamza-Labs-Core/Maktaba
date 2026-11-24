//go:build integration

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/broadcastgate"
	moderationh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/moderation"
)

// Home broadcasting is off (broadcast.enabled = false, the default): every broadcast and
// moderation route of the api role's real routers answers what an unknown path answers, whoever
// asks, but broadcastgate's exceptions.

// routeRow is one registered route of a listener.
type routeRow struct {
	listener, method, pattern, handler string
}

// routeTable walks both listeners' routers.
func routeTable(t *testing.T, e *env) []routeRow {
	t.Helper()
	var out []routeRow
	for name, h := range map[string]http.Handler{"user": e.router, "console": e.admin} {
		mux, ok := h.(chi.Routes)
		if !ok {
			t.Fatalf("the %s listener is a %T", name, h)
		}
		if err := chi.Walk(mux, func(method, pattern string, h http.Handler, _ ...func(http.Handler) http.Handler) error {
			out = append(out, routeRow{name, method, pattern, handlerName(h)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(out) < 100 {
		t.Fatalf("only %d routes", len(out))
	}
	return out
}

// handlerName is the Go name of a route's handler, behind its inline middleware.
func handlerName(h http.Handler) string {
	if c, ok := h.(*chi.ChainHandler); ok {
		h = c.Endpoint
	}
	if f, ok := h.(http.HandlerFunc); ok {
		return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
	}
	return reflect.TypeOf(h).String()
}

// aboutBroadcasting: a handler of the broadcast, edge or moderation packages, or any with
// broadcasting in its name or path.
var aboutBroadcasting = regexp.MustCompile(`(?i)handlers/(broadcast|edge|moderation)\.|broadcast|moderation|takedown|geodeny|/_edge`)

func (r routeRow) aboutBroadcasting() bool {
	return aboutBroadcasting.MatchString(r.handler) || aboutBroadcasting.MatchString(r.pattern)
}

// A broadcast or moderation handler is mounted only under the gate's paths, so a new one is
// closed without doing anything; every exception names a route that exists.
func TestBroadcastRoutesAreAllBehindTheGate(t *testing.T) {
	rows := routeTable(t, newEnv(t))
	n := 0
	have := map[string]bool{}
	for _, r := range rows {
		have[r.method+" "+r.pattern] = true
		if !r.aboutBroadcasting() {
			if broadcastgate.Covers(r.pattern) {
				t.Errorf("%s %s (%s) is under the gate but not about broadcasting", r.method, r.pattern, r.handler)
			}
			continue
		}
		n++
		if !broadcastgate.Covers(r.pattern) {
			t.Errorf("%s listener: %s %s (%s) answers while broadcasting is off: mount it under one of %v",
				r.listener, r.method, r.pattern, r.handler, broadcastgate.Paths)
		}
	}
	if n < 55 {
		t.Errorf("only %d broadcast routes found: the table no longer sees them", n)
	}
	for _, ex := range broadcastgate.Exceptions() {
		if !have[ex.Method+" "+ex.Path] {
			t.Errorf("the exception %s %s names no route", ex.Method, ex.Path)
		}
	}
}

// raw sends one request to a listener as the console or the portal would.
func (e *env) raw(listener string, req *http.Request) *httptest.ResponseRecorder {
	h := e.router
	if listener == "console" {
		h = e.admin
		req.Host = testAdminHost
		req.Header.Set("Origin", "https://"+testAdminHost)
		req.Header.Set("X-Maktaba-Staff", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var routeParam = regexp.MustCompile(`\{[^}]+\}`)

// Every route, asked by everyone who could reach it while it is on, is the same 404 as a path
// that doesn't exist: no credential, a home (its secret right), its owner, a service admin, the
// edge's signature, each with an id that exists and one that doesn't. Nothing is written.
func TestBroadcastOffAnswersNothing(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Broadcast.KeyPath = ingestKeyFile(t) // the keys are there: only the switch is off
		c.Broadcast.EdgePublicKey = base64.RawURLEncoding.EncodeToString(pub)
		c.Broadcast.IngestURL = ""
	})
	owner := e.registerVerified("off@example.test")
	home := claimHome(e, owner, "offhome")
	setPlan(e, e.userID("off@example.test"), "pro")
	// another home is marked: its mark opens nothing for this one
	marked := claimHome(e, e.registerVerified("marked@example.test"), "markedhome")
	e.exec(`UPDATE servers SET broadcast_test_publisher = true WHERE id = $1`, marked.id)
	ops := e.serviceAdmin("ops@example.test")

	unknown := map[string]*httptest.ResponseRecorder{}
	for _, l := range []string{"user", "console"} {
		unknown[l] = e.raw(l, httptest.NewRequest("GET", "https://api.example.test/v1/no-such-route", nil))
		if unknown[l].Code != http.StatusNotFound {
			t.Fatalf("an unknown path on %s = %d", l, unknown[l].Code)
		}
	}
	type caller struct {
		who  broadcastgate.Who
		sign func(*http.Request, []byte)
	}
	callers := map[string]caller{
		"nobody": {"", func(*http.Request, []byte) {}},
		"a home that isn't marked": {"", func(r *http.Request, _ []byte) {
			r.SetBasicAuth(home.id, home.secret)
		}},
		"a marked home's id with a wrong secret": {"", func(r *http.Request, _ []byte) { r.SetBasicAuth(marked.id, home.secret) }},
		"an owner":                               {"", func(r *http.Request, _ []byte) { r.Header.Set("Authorization", "Bearer "+owner.token) }},
		"a service admin": {broadcastgate.Staff, func(r *http.Request, _ []byte) {
			r.Header.Set("Authorization", "Bearer "+ops.token)
			for k, v := range ops.header {
				r.Header.Set(k, v)
			}
		}},
		"the edge": {broadcastgate.Edge, func(r *http.Request, body []byte) {
			broadcast.SignEdgeRequest(priv, r.Header, r.Method, r.URL.Path, time.Now().UnixMilli(), body)
		}},
		"a bad edge signature": {"", func(r *http.Request, _ []byte) {
			r.Header.Set(broadcast.HeaderEdgeSignature, "AAAA")
		}},
	}
	open := map[string]broadcastgate.Who{}
	for _, ex := range broadcastgate.Exceptions() {
		open[ex.Method+" "+ex.Path] = ex.Who
	}
	asked := 0
	for _, r := range routeTable(t, e) {
		if !broadcastgate.Covers(r.pattern) {
			continue
		}
		for name, c := range callers {
			switch who := open[r.method+" "+r.pattern]; {
			case who == broadcastgate.Staff && r.listener == "console":
				continue // the staff gate answers (TestBroadcastOffKeepsTheKillSwitch)
			case who == broadcastgate.Edge && c.who == broadcastgate.Edge, who == broadcastgate.EdgeOrHome && c.who == broadcastgate.Edge:
				continue // the edge's own (below)
			}
			for _, id := range []string{home.id, "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60"} {
				path := routeParam.ReplaceAllString(r.pattern, id)
				body := []byte(`{}`)
				if r.method == http.MethodGet {
					body = nil
				}
				for _, method := range []string{r.method, http.MethodTrace, http.MethodHead} {
					req := httptest.NewRequest(method, "https://api.example.test"+path, bytes.NewReader(body))
					c.sign(req, body)
					rec := e.raw(r.listener, req)
					asked++
					want := unknown[r.listener]
					if rec.Code != want.Code || rec.Body.String() != want.Body.String() || !sameHeaders(rec.Header(), want.Header()) {
						t.Fatalf("%s: %s listener %s %s = %d %q %v, want an unknown path's %d %q %v", name, r.listener, method, path,
							rec.Code, rec.Body, rec.Header(), want.Code, want.Body, want.Header())
					}
				}
			}
		}
	}
	if asked < 1000 {
		t.Errorf("only %d requests asked", asked)
	}
	for _, table := range []string{"broadcast_channels", "broadcast_invites", "broadcast_agreements", "moderation_notices",
		"moderation_reports", "broadcast_usage_windows"} {
		if n := e.count(`SELECT count(*) FROM ` + table); n != 0 {
			t.Errorf("%s has %d rows", table, n)
		}
	}
	for name, path := range map[string]string{"encoded": "/v1/%62roadcast/state", "encoded id": "/v1/broadcast/channels/%2e%2e",
		"longer": "/v1/broadcast-new/x", "trailing": "/v1/moderation/notices/"} {
		req := httptest.NewRequest("GET", "https://api.example.test"+path, nil)
		req.SetBasicAuth(marked.id, marked.secret)
		if rec := e.raw("user", req); rec.Code != http.StatusNotFound || rec.Body.String() != unknown["user"].Body.String() {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// a preflight, which the platform answers on any path before the gate, is the unknown path's
	// own from every origin: the web app's, the forms', another site's, none
	for _, origin := range []string{"", "https://maktaba.tv", "https://app.maktaba.tv", moderationh.WatchOrigins[0],
		moderationh.StagingWatchOrigin, "https://evil.test", "https://" + testAdminHost} {
		preflight := func(listener, path string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodOptions, "https://api.example.test"+path, nil)
			req.Header.Set("Access-Control-Request-Method", "POST")
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			h := e.router
			if listener == "console" {
				h, req.Host = e.admin, testAdminHost
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		}
		for _, r := range routeTable(t, e) {
			if !broadcastgate.Covers(r.pattern) {
				continue
			}
			path := routeParam.ReplaceAllString(r.pattern, home.id)
			got, want := preflight(r.listener, path), preflight(r.listener, "/v1/no-such-route")
			if got.Code != want.Code || got.Body.String() != want.Body.String() || !sameHeaders(got.Header(), want.Header()) {
				t.Errorf("a preflight of %s from %q on %s = %d %v, want an unknown path's %d %v", path, origin, r.listener,
					got.Code, got.Header(), want.Code, want.Header())
			}
		}
	}
	// the account's own data still answers: the export, and deletion
	owner.mustStatus(owner.do("GET", "/v1/account/export", nil), http.StatusOK)
}

func sameHeaders(a, b http.Header) bool {
	a, b = a.Clone(), b.Clone()
	for _, h := range []http.Header{a, b} {
		h.Del("X-Request-Id")
		h.Del("Date")
	}
	return reflect.DeepEqual(a, b)
}

// What keeps "off" safe: the edge still reads the kill switch's whole list, and a test publisher's
// home its own copy, no one else; the console still changes it, staff only; and the edge's calls
// know a test publisher and no one else.
func TestBroadcastOffKeepsTheKillSwitch(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keyFile := ingestKeyFile(t)
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Broadcast.KeyPath = keyFile
		c.Broadcast.EdgePublicKey = base64.RawURLEncoding.EncodeToString(pub)
		c.Broadcast.IngestURL = ""
	})
	key, err := broadcast.LoadKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PublicKey{key.Kid: key.Priv.Public().(ed25519.PublicKey)}
	owner := e.registerVerified("kill@example.test")
	home := claimHome(e, owner, "killhome")
	setPlan(e, e.userID("kill@example.test"), "pro")
	plain := claimHome(e, owner, "plainhome")
	setPlan(e, e.userID("kill@example.test"), "pro")
	ops := e.serviceAdmin("ops@example.test")
	const list = "/v1/admin/broadcast/revocations"

	if r := owner.do("GET", list, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("a customer on the kill switch = %d", r.code)
	}
	if r := e.staff("norole@example.test", "").do("GET", list, nil); r.code != http.StatusForbidden {
		t.Fatalf("staff without the role on the kill switch = %d", r.code)
	}
	r := ops.mustStatus(ops.do("POST", list, map[string]any{"kind": "home", "ref": home.id, "action": "suspend", "reason": "abuse"}), http.StatusCreated).json(t)
	id := r["entry"].(map[string]any)["id"].(string)
	// another home's: in the edge's list, never in this home's copy
	ops.mustStatus(ops.do("POST", list, map[string]any{"kind": "home", "ref": plain.id, "action": "suspend", "reason": "abuse"}), http.StatusCreated)

	signed := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://api.example.test"+path, bytes.NewReader(body))
		broadcast.SignEdgeRequest(priv, req.Header, method, path, time.Now().UnixMilli(), body)
		return e.raw("user", req)
	}
	if rec := signed("GET", "/v1/broadcast/revocations", nil); rec.Code != http.StatusOK || rec.Header().Get("X-Maktaba-Revocations-Version") != "2" {
		t.Fatalf("the edge's pull while off = %d %s", rec.Code, rec.Body)
	}
	if r := home.agent.do("GET", "/v1/broadcast/revocations", nil); r.code != http.StatusNotFound {
		t.Fatalf("an unmarked home's pull while off = %d", r.code)
	}

	// a publisher's state and usage: unknown until the home is marked
	usage := func(server string) map[string]any {
		start := time.Now().UTC().Truncate(5 * time.Minute).Add(-5 * time.Minute)
		key := server + ":" + start.Format(time.RFC3339)
		body, _ := json.Marshal(broadcast.UsagePost{Edge: "test", Windows: []broadcast.UsageWindow{{Key: key, Publisher: server,
			WindowStart: start, WindowEnd: start.Add(5 * time.Minute), MinutesFull: 3}}})
		rec := signed("POST", "/_edge/usage", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("the edge's usage post = %d %s", rec.Code, rec.Body)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if rec := signed("GET", "/_edge/publishers/"+home.id, nil); rec.Code != http.StatusNotFound || rec.Body.String() != "404 page not found\n" {
		t.Fatalf("an unmarked publisher's state = %d %s", rec.Code, rec.Body)
	}
	for _, server := range []string{home.id, "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60"} {
		if out := usage(server); len(out["taken"].([]any)) != 0 || len(out["rejected"].([]any)) != 1 || len(out["states"].([]any)) != 0 {
			t.Fatalf("usage of %s while off = %v", server, out)
		}
	}
	// the mark is a service admin's, with a reason
	mark := func(server string, on bool) {
		ops.mustStatus(ops.do("POST", "/v1/admin/servers/"+server+"/broadcast",
			map[string]any{"plan": "", "test_publisher": on, "reason": "rehearsal"}), http.StatusNoContent)
	}
	mark(home.id, true)
	copied, err := broadcast.VerifyHomeRevocations(keys, string(home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/revocations", nil), http.StatusOK).body))
	if err != nil || len(copied.Entries) != 1 || copied.Entries[0].Ref != home.id {
		t.Fatalf("a marked home's copy = %+v, %v, want its own entry only", copied, err)
	}
	home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/state", nil), http.StatusOK)
	home.agent.mustStatus(home.agent.do("GET", "/v1/broadcast/invites", nil), http.StatusOK)
	owner.mustStatus(owner.do("GET", "/v1/broadcast/agreement", nil), http.StatusOK)
	if rec := signed("GET", "/_edge/publishers/"+home.id, nil); rec.Code != http.StatusOK {
		t.Fatalf("a marked publisher's state = %d %s", rec.Code, rec.Body)
	}
	if out := usage(home.id); len(out["taken"].([]any)) != 1 || len(out["states"].([]any)) != 1 {
		t.Fatalf("a marked publisher's usage = %v", out)
	}
	if out := usage(plain.id); len(out["taken"].([]any)) != 0 {
		t.Fatalf("its owner's other home's usage = %v", out)
	}
	// each of the marked home's own routes reaches its handler, which answers as it does with
	// broadcasting on, and with another home's secret is the gate's
	answers := map[string]int{
		"GET /v1/broadcast/revocations":                    http.StatusOK,
		"GET /v1/broadcast/state":                          http.StatusOK,
		"GET /v1/broadcast/access":                         http.StatusOK,
		"GET /v1/broadcast/invites":                        http.StatusOK,
		"PUT /v1/broadcast/viewer-limits":                  http.StatusNoContent,
		"POST /v1/broadcast/channels":                      http.StatusBadRequest, // no slug
		"POST /v1/broadcast/ingest-token":                  http.StatusBadRequest, // no channel
		"POST /v1/broadcast/invites":                       http.StatusCreated,
		"DELETE /v1/broadcast/channels/{id}":               http.StatusNotFound, // the handler's own JSON
		"PUT /v1/broadcast/channels/{id}/access":           http.StatusNotFound,
		"DELETE /v1/broadcast/invites/{id}":                http.StatusNotFound,
		"GET /v1/broadcast/invites/{id}/devices":           http.StatusNotFound,
		"DELETE /v1/broadcast/invites/{id}/devices/{pass}": http.StatusNotFound,
	}
	for _, ex := range broadcastgate.Exceptions() {
		if ex.Who != broadcastgate.Home && ex.Who != broadcastgate.EdgeOrHome {
			continue
		}
		path := routeParam.ReplaceAllString(ex.Path, "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60")
		for secret, want := range map[string]bool{home.secret: true, plain.secret: false} {
			req := httptest.NewRequest(ex.Method, "https://api.example.test"+path, strings.NewReader(`{}`))
			req.SetBasicAuth(home.id, secret)
			rec := e.raw("user", req)
			code, known := answers[ex.Method+" "+ex.Path]
			reached := rec.Body.String() != "404 page not found\n"
			if reached != want || (want && (!known || rec.Code != code)) || (!want && rec.Code != http.StatusNotFound) {
				t.Errorf("a marked home's %s %s: reached %v, %d %s, want %v, %d", ex.Method, path, reached, rec.Code, rec.Body, want, code)
			}
		}
	}
	// the mark opens nothing more: the owner's access page, analytics, billing and moderation
	for _, p := range []string{"/v1/broadcast/homes/" + home.id + "/access", "/v1/broadcast/analytics?server=" + home.id,
		"/v1/billing/broadcast?server_id=" + home.id} {
		if r := owner.do("GET", p, nil); r.code != http.StatusNotFound {
			t.Errorf("the owner's %s = %d", p, r.code)
		}
	}
	for _, p := range []string{"/v1/broadcast/home/analytics", "/v1/broadcast/takedowns", "/v1/broadcast/moderation"} {
		if r := home.agent.do("GET", p, nil); r.code != http.StatusNotFound {
			t.Errorf("the marked home's %s = %d", p, r.code)
		}
	}
	if r := plain.agent.do("GET", "/v1/broadcast/state", nil); r.code != http.StatusNotFound {
		t.Errorf("its owner's other home = %d", r.code)
	}
	// unmarked again: closed at once; and the kill switch lifts
	mark(home.id, false)
	if r := home.agent.do("GET", "/v1/broadcast/state", nil); r.code != http.StatusNotFound {
		t.Errorf("unmarked = %d", r.code)
	}
	if r := owner.do("GET", "/v1/broadcast/agreement", nil); r.code != http.StatusNotFound {
		t.Errorf("the owner of no marked home = %d", r.code)
	}
	ops.mustStatus(ops.do("POST", list+"/"+id+"/lift", map[string]any{"note": "rehearsal over"}), http.StatusOK)
}
