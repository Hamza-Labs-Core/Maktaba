package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/storelink"
)

// Story 54.19 §5, §6 (the API's half): the refresh asks the store with the home's signature,
// has the supervisor check what it answered, and replaces plugin_entitlements with what
// holds; a refunded purchase the store no longer lists goes; any failure keeps the rows.

type fakeLink struct {
	linked bool
	status int
	answer string
	paths  []string
	err    error
}

func (l *fakeLink) LoadState(context.Context) (*storelink.State, error) {
	if !l.linked {
		return nil, nil
	}
	return &storelink.State{HomeID: "h1"}, nil
}
func (l *fakeLink) KeyFingerprint(context.Context) (string, error) { return "ab", nil }
func (l *fakeLink) Signed(_ context.Context, method, path string, _ []byte) (*http.Response, []byte, error) {
	l.paths = append(l.paths, method+" "+path)
	if l.err != nil {
		return nil, nil, l.err
	}
	return &http.Response{StatusCode: l.status}, []byte(l.answer), nil
}

type fakeVerifier struct {
	got  EntitlementsCheck
	keep []string // purchase ids that hold
	err  error
}

func (v *fakeVerifier) VerifyEntitlements(_ context.Context, c EntitlementsCheck) (*EntitlementsChecked, error) {
	v.got = c
	if v.err != nil {
		return nil, v.err
	}
	out := &EntitlementsChecked{}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for i, raw := range c.Entitlements {
		var env struct {
			Signed struct {
				PluginID   string `json:"plugin_id"`
				PurchaseID string `json:"purchase_id"`
			} `json:"signed"`
		}
		_ = json.Unmarshal(raw, &env)
		ok := false
		for _, k := range v.keep {
			ok = ok || k == env.Signed.PurchaseID
		}
		if !ok {
			out.Refused = append(out.Refused, RefusedEntitlement{Index: i, Code: "entitlement_other_home", Step: "entitlement"})
			continue
		}
		out.Valid = append(out.Valid, CheckedEntitlement{PluginID: env.Signed.PluginID, PurchaseID: env.Signed.PurchaseID,
			Product: "one_time", ValidFrom: now, ValidUntil: now.Add(31 * 24 * time.Hour), IssuedAt: now, Envelope: raw})
	}
	return out, nil
}

func answer(purchases ...string) string {
	list := []map[string]any{}
	for _, p := range purchases {
		list = append(list, map[string]any{"signed": map[string]string{"plugin_id": "dev.example." + p, "purchase_id": p},
			"signatures": []any{}})
	}
	b, _ := json.Marshal(map[string]any{"home_id": "h1", "entitlements": list})
	return string(b)
}

func TestEntitlementsRefresh(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	link := &fakeLink{linked: true, status: 200, answer: answer("p1", "p2", "p3")}
	ver := &fakeVerifier{keep: []string{"p1", "p2"}}
	e := &Entitlements{DB: db, Link: link, Verifier: ver}
	count := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM plugin_entitlements`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	res, err := e.Refresh(ctx)
	if err != nil || res.Valid != 2 || len(res.Refused) != 1 || count() != 2 {
		t.Fatalf("refresh %+v %v (%d rows)", res, err, count())
	}
	if link.paths[0] != "GET /v1/homes/h1/entitlements" || ver.got.HomeID != "h1" || ver.got.HomeKeyFP != "ab" || len(ver.got.Entitlements) != 3 {
		t.Fatalf("asked %v, checked %+v", link.paths, ver.got)
	}
	// p2 refunded: the store no longer lists it
	link.answer = answer("p1")
	if _, err := e.Refresh(ctx); err != nil || count() != 1 {
		t.Fatalf("after a refund: %v, %d rows", err, count())
	}
	// a store outage, an error answer, another home's answer or no store keys: rows kept
	for name, set := range map[string]func(){
		"unreachable":  func() { link.err = errors.New("dial: no route") },
		"503":          func() { link.err, link.status = nil, 503 },
		"another home": func() { link.status, link.answer = 200, `{"home_id":"h9","entitlements":[]}` },
		"no keys":      func() { link.answer, ver.err = answer(), errors.New("store_keys_unavailable") },
	} {
		set()
		if _, err := e.Refresh(ctx); err == nil || count() != 1 {
			t.Fatalf("%s: %v, %d rows", name, err, count())
		}
	}
	// not linked: nothing asked, nothing changed
	link.linked, link.err, ver.err = false, nil, nil
	asked := len(link.paths)
	if res, err := e.Refresh(ctx); err != nil || res.Linked || len(link.paths) != asked || count() != 1 {
		t.Fatalf("not linked: %+v %v", res, err)
	}
	list, err := e.List(ctx)
	if err != nil || len(list) != 1 || list[0]["plugin_id"] != "dev.example.p1" {
		t.Fatalf("list %v %v", list, err)
	}
}

func TestEntitlementsBurst(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	e := &Entitlements{Now: func() time.Time { return now }}
	if d := e.next(); d < EntitlementsEvery || d > EntitlementsEvery+time.Hour {
		t.Fatalf("daily: %v", d)
	}
	e.Burst()
	if d := e.next(); d != EntitlementsBurst {
		t.Fatalf("after a Buy: %v", d)
	}
	now = now.Add(EntitlementsBurstFor + time.Second)
	if d := e.next(); d < EntitlementsEvery {
		t.Fatalf("after the burst: %v", d)
	}
}
