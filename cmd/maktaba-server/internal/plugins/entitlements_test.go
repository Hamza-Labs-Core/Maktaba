package plugins

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Story 54.19 §6: the supervisor checks the entitlements the API fetched against the store
// keys it verified: an entitlement key's, for this home and key, valid now. A store key's
// signature, another home's or a lapsed one is refused with its code; with no verified
// store-keys.json nothing is checked (503).
func TestVerifyEntitlementsRoute(t *testing.T) {
	k := newKit()
	ent := testSigner("entitlement-1")
	list := []pluginsig.StoreKey{
		{ID: k.store1.ID, Public: k.store1.Public(), Use: pluginsig.UseStore,
			NotBefore: pluginsig.FormatTime(k.now.Add(-720 * time.Hour)), NotAfter: pluginsig.FormatTime(k.now.Add(8760 * time.Hour))},
		{ID: ent.ID, Public: ent.Public(), Use: pluginsig.UseEntitlement,
			NotBefore: pluginsig.FormatTime(k.now.Add(-720 * time.Hour)), NotAfter: pluginsig.FormatTime(k.now.Add(8760 * time.Hour))},
	}
	keys, err := pluginsig.SignEnvelope(pluginsig.KindStoreKeys, pluginsig.StoreKeysSigned{Version: 3,
		Expires: pluginsig.FormatTime(k.now.Add(300 * 24 * time.Hour)), Keys: list}, k.root)
	if err != nil {
		t.Fatal(err)
	}
	f := k.files(t, keys, pluginsig.State{})
	body := func(over map[string]any) map[string]any {
		b := map[string]any{"v": 1, "home_id": "h1", "home_key_fp": "ab", "plugin_id": "dev.example.loudness",
			"product": "one_time", "purchase_id": "pur-1", "valid_from": pluginsig.FormatTime(k.now.Add(-24 * time.Hour)),
			"valid_until": pluginsig.FormatTime(k.now.Add(31 * 24 * time.Hour)), "issued_at": pluginsig.FormatTime(k.now)}
		for kk, v := range over {
			b[kk] = v
		}
		return b
	}
	sign := func(b map[string]any, by pluginsig.Signer) json.RawMessage {
		raw, err := pluginsig.SignEnvelope(pluginsig.KindEntitlement, b, by)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	req := EntitlementsRequest{HomeID: "h1", HomeKeyFP: "ab", Entitlements: []json.RawMessage{
		sign(body(nil), ent),
		sign(body(nil), k.store1),
		sign(body(map[string]any{"home_id": "h2"}), ent),
		sign(body(map[string]any{"valid_until": pluginsig.FormatTime(k.now.Add(-100 * time.Hour))}), ent),
	}}
	rt := &Routes{Files: f, Token: func() string { return "tok" }}
	h := rt.Handler(http.NotFoundHandler())
	post := func(v any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(v)
		r := httptest.NewRequest(http.MethodPost, "/plugins/entitlements/verify", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer tok")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := post(req)
	var out struct {
		Valid   []CheckedEntitlement `json:"valid"`
		Refused []RefusedEntitlement `json:"refused"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(out.Valid) != 1 || out.Valid[0].PluginID != "dev.example.loudness" || out.Valid[0].PurchaseID != "pur-1" ||
		out.Valid[0].ValidUntil == "" || len(out.Valid[0].Envelope) == 0 {
		t.Fatalf("valid %+v", out.Valid)
	}
	codes := map[int]string{}
	for _, r := range out.Refused {
		codes[r.Index] = r.Code
	}
	if codes[1] != pluginsig.CodeKeyUseMismatch || codes[2] != pluginsig.CodeEntitlementOtherHome || codes[3] != pluginsig.CodeEntitlementExpired {
		t.Fatalf("refused %+v", out.Refused)
	}
	if w := post(EntitlementsRequest{HomeKeyFP: "ab"}); w.Code != 400 {
		t.Fatalf("no home id: %d", w.Code)
	}
	// no verified store-keys.json: nothing is checked
	none := &Routes{Files: k.files(t, nil, pluginsig.State{}), Token: func() string { return "tok" }}
	b, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/plugins/entitlements/verify", bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	none.Handler(http.NotFoundHandler()).ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no store keys: %d %s", rec.Code, rec.Body)
	}
}
