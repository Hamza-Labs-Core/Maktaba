package broadcast

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

type fakeSource struct {
	version int64
	entries []bc.Entry
}

func (f fakeSource) Current(context.Context) (int64, []bc.Entry, error) {
	return f.version, f.entries, nil
}

func revKey(t *testing.T, label string) bc.Key {
	t.Helper()
	seed := sha256.Sum256([]byte("maktaba-broadcast TEST KEY, never for production: " + label))
	k, err := bc.KeyFromSeed(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func revRig(t *testing.T, withKey bool) (*chi.Mux, *RevocationDeps, bc.Key, ed25519.PrivateKey) {
	t.Helper()
	k := revKey(t, "cloud-1")
	edge := revKey(t, "edge-1")
	src := fakeSource{version: 7, entries: []bc.Entry{
		{ID: "r-3", Kind: bc.KindGlobal, Action: bc.ActionOffAir, Reason: bc.ReasonIncident, Since: at},
		{ID: "r-4", Kind: bc.KindHome, Ref: "home-1", Action: bc.ActionSuspend, Reason: bc.ReasonReport, Since: at},
		{ID: "r-5", Kind: bc.KindHome, Ref: "home-2", Action: bc.ActionSuspend, Reason: bc.ReasonAbuse, Since: at},
		{ID: "r-6", Kind: bc.KindPublisher, Ref: "owner-1", Action: bc.ActionSuspend, Reason: bc.ReasonReport, Since: at},
		{ID: "r-7", Kind: bc.KindPublisher, Ref: "owner-2", Action: bc.ActionTerminate, Reason: bc.ReasonRepeatInfringer, Since: at},
		{ID: "r-8", Kind: bc.KindChannel, Ref: "0123abcd", Action: bc.ActionOffAir, Reason: bc.ReasonNotice, Since: at},
	}}
	rev := &bc.Revocations{Store: src, Now: func() time.Time { return at }}
	if withKey {
		rev.Key = &k
	}
	d := &RevocationDeps{Rev: rev, Agents: fakeAgents{"home-1": "s3cret"},
		EdgeKey: edge.Priv.Public().(ed25519.PublicKey), Now: func() time.Time { return at },
		Owner: func(_ context.Context, id string) (string, error) {
			return map[string]string{"home-1": "owner-1"}[id], nil
		}}
	r := chi.NewRouter()
	MountRevocations(r, d)
	MountRevocationsAdmin(r, d)
	return r, d, k, edge.Priv
}

func edgeGet(priv ed25519.PrivateKey, ms int64) *http.Request {
	req := httptest.NewRequest("GET", RevocationsPath, nil)
	req.Header.Set(bc.HeaderEdgeTime, strconv.FormatInt(ms, 10))
	req.Header.Set(bc.HeaderEdgeSignature, base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(bc.EdgeSigningInput("GET", RevocationsPath, ms, nil)))))
	return req
}

func serve(r http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The pull: the edge's signed GET and a home's secret get the signed list; anything else is 401;
// with no signing key it is 503.
func TestThePullServesTheEdgeAndHomesOnly(t *testing.T) {
	r, _, k, edgePriv := revRig(t, true)
	pubs := map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}

	rec := serve(r, edgeGet(edgePriv, at.UnixMilli()))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Maktaba-Revocations-Version") != "7" {
		t.Fatalf("the edge's pull = %d %s", rec.Code, rec.Body)
	}
	l, err := bc.VerifyRevocations(pubs, rec.Body.String())
	if err != nil || l.Version != 7 || len(l.Entries) != 6 {
		t.Fatalf("the edge's list = %+v %v", l, err)
	}

	// a home's copy: the global and channel entries, and its own home and publisher entries;
	// never another home's server id, another account's id, or the reasons given about them
	home := httptest.NewRequest("GET", RevocationsPath, nil)
	home.SetBasicAuth("home-1", "s3cret")
	rec = serve(r, home)
	if rec.Code != http.StatusOK {
		t.Fatalf("a home's pull = %d", rec.Code)
	}
	l, err = bc.VerifyRevocations(pubs, rec.Body.String())
	var ids []string
	for _, e := range l.Entries {
		ids = append(ids, e.ID)
	}
	if err != nil || l.Version != 7 || strings.Join(ids, ",") != "r-3,r-4,r-6,r-8" {
		t.Fatalf("the home's list = %v %v", ids, err)
	}

	wrongSecret := httptest.NewRequest("GET", RevocationsPath, nil)
	wrongSecret.SetBasicAuth("home-1", "nope")
	stale := edgeGet(edgePriv, at.Add(-10*time.Minute).UnixMilli())
	forged := edgeGet(revKey(t, "someone-else").Priv, at.UnixMilli())
	for name, req := range map[string]*http.Request{
		"no credential":  httptest.NewRequest("GET", RevocationsPath, nil),
		"a wrong secret": wrongSecret, "a stale edge signature": stale, "another key's signature": forged,
	} {
		if rec := serve(r, req); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}

	off, _, _, _ := revRig(t, false)
	if rec := serve(off, home); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no signing key = %d", rec.Code)
	}
}

// With no gate the admin routes are 403; past a gate that names no staff account, still 403
// (a handler never changes the list without an actor to audit).
func TestTheAdminRoutesNeedTheGateAndAnActor(t *testing.T) {
	r, d, _, _ := revRig(t, true)
	for _, rt := range [][2]string{{"GET", "/v1/admin/broadcast/revocations"}, {"POST", "/v1/admin/broadcast/revocations"}, {"POST", "/v1/admin/broadcast/revocations/r-1/lift"}} {
		if rec := serve(r, httptest.NewRequest(rt[0], rt[1], strings.NewReader(`{}`))); rec.Code != http.StatusForbidden {
			t.Errorf("no gate %s %s = %d", rt[0], rt[1], rec.Code)
		}
	}
	// a gate that lets everything through but sets no operator
	d.Gate = func(next http.Handler) http.Handler { return next }
	open := chi.NewRouter()
	MountRevocationsAdmin(open, d)
	for _, path := range []string{"/v1/admin/broadcast/revocations", "/v1/admin/broadcast/revocations/r-1/lift"} {
		if rec := serve(open, httptest.NewRequest("POST", path, strings.NewReader(`{}`))); rec.Code != http.StatusForbidden {
			t.Errorf("no operator POST %s = %d", path, rec.Code)
		}
	}
	// an operator with a bad entry: 400 before any store is touched
	req := httptest.NewRequest("POST", "/v1/admin/broadcast/revocations", strings.NewReader(`{"kind":"global","ref":"x","action":"off_air","reason":"incident"}`))
	req = req.WithContext(authmw.WithOperator(req.Context(), authmw.Operator{ID: "staff-1", Email: "ops@example.test"}))
	if rec := serve(open, req); rec.Code != http.StatusBadRequest {
		t.Errorf("a global entry with a ref = %d %s", rec.Code, rec.Body)
	}
	past := httptest.NewRequest("POST", "/v1/admin/broadcast/revocations", strings.NewReader(`{"kind":"programme","ref":"c","action":"slate","reason":"notice","until":"2026-11-13T11:00:00Z"}`))
	past = past.WithContext(authmw.WithOperator(past.Context(), authmw.Operator{ID: "staff-1"}))
	if rec := serve(open, past); rec.Code != http.StatusBadRequest {
		t.Errorf("a programme ending in the past = %d %s", rec.Code, rec.Body)
	}
	if rec := serve(open, httptest.NewRequest("POST", "/v1/admin/broadcast/revocations/not-an-id/lift", nil).WithContext(
		authmw.WithOperator(context.Background(), authmw.Operator{ID: "staff-1"}))); rec.Code != http.StatusNotFound {
		t.Errorf("a lift of no entry id = %d", rec.Code)
	}
}

// The push: the signed list goes to the edge's control route; a 409 (the edge holds a newer
// one) is fine; a failing edge is tried three times and reported.
func TestThePushSendsTheSignedList(t *testing.T) {
	k := revKey(t, "cloud-1")
	var got atomic.Value
	var calls atomic.Int32
	status := atomic.Int32{}
	status.Store(http.StatusOK)
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.URL.Path != "/v1/control/revocations" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got.Store(string(b))
		w.WriteHeader(int(status.Load()))
	}))
	defer edge.Close()
	rev := &bc.Revocations{Store: fakeSource{version: 3}, Key: &k, ControlURL: edge.URL + "/v1/control/revocations", Now: func() time.Time { return at }}
	if err := rev.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	l, err := bc.VerifyRevocations(map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}, got.Load().(string))
	if err != nil || l.Version != 3 {
		t.Fatalf("the edge got %+v %v", l, err)
	}
	status.Store(http.StatusConflict)
	if err := rev.Push(context.Background()); err != nil {
		t.Errorf("a 409 (a newer list at the edge) = %v", err)
	}
	status.Store(http.StatusInternalServerError)
	calls.Store(0)
	if err := rev.Push(context.Background()); err == nil || calls.Load() != 3 {
		t.Errorf("a failing edge: err %v after %d calls", err, calls.Load())
	}
	// no control URL or no key: nothing to push, no error
	if err := (&bc.Revocations{Store: fakeSource{}, Key: &k}).Push(context.Background()); err != nil {
		t.Error(err)
	}
}
