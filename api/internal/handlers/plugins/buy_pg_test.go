package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/storelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// Story 54.19 §5 (the home's half): Buy asks the store for a purchase intent signed by this
// home with the plugin and the language only (never the home's address), and passes on the
// store's buy page for that plugin; an unlinked home is told to link; a store answer that
// points anywhere else is refused; offline, nothing goes out.
func TestBuyAsksTheStoreForAnIntentSigned(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	var sent []map[string]any
	var paths, auths []string
	answer := func(storeURL string) string {
		return `{"intent":"` + strings.Repeat("A", 43) + `","buy_url":"` + storeURL + `/buy/dev.example.paid","product":"one_time","expires_at":"2026-11-07T12:10:00Z"}`
	}
	var storeURL string
	evil := false
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		sent, paths, auths = append(sent, m), append(paths, r.URL.Path), append(auths, r.Header.Get("Authorization"))
		if m["plugin_id"] == "dev.example.free" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_for_sale"}`))
			return
		}
		if evil {
			_, _ = w.Write([]byte(strings.Replace(answer(storeURL), storeURL, "https://evil.example", 1)))
			return
		}
		_, _ = w.Write([]byte(answer(storeURL)))
	}))
	defer store.Close()
	storeURL = store.URL
	link := &storelink.Link{DB: db, Secrets: secrets.NewMemory(), StoreURL: store.URL}
	ents := &pl.Entitlements{DB: db, Link: link}
	h := &Admin{L: &pl.Lifecycle{DB: db}, Store: link, Entitlements: ents}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/plugins/store-account/buy", bytes.NewBufferString(body))
		req.Header.Set("X-Forwarded-Host", "maktaba.example.home:8080")
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a1", IsAdmin: true}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := call(`{"plugin_id":"dev.example.paid"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "store_not_linked") {
		t.Fatalf("unlinked: %d %s", w.Code, w.Body)
	}
	_ = link.Secrets.Put(context.Background(), storelink.KeyName, bytes.Repeat([]byte{7}, 32), "test")
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, '{"home_id":"h_1","linked_at":"2026-11-06T00:00:00Z"}')`, storelink.StateKey); err != nil {
		t.Fatal(err)
	}
	if w := call(`{"plugin_id":"../../x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a bad id: %d %s", w.Code, w.Body)
	}
	w := call(`{"plugin_id":"dev.example.paid","lang":"ar"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("buy: %d %s", w.Code, w.Body)
	}
	var ans BuyAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &ans)
	if ans.BuyURL != store.URL+"/buy/dev.example.paid" || len(ans.Intent) != 43 || ans.Product != "one_time" {
		t.Fatalf("answer %+v", ans)
	}
	if paths[0] != "/v1/homes/h_1/purchase-intents" || !strings.HasPrefix(auths[0], "MKS-Home id=h_1, ") {
		t.Fatalf("sent to %s, %q", paths[0], auths[0])
	}
	// the plugin and the language, nothing else (no address of the home)
	for k := range sent[0] {
		if k != "plugin_id" && k != "lang" {
			t.Fatalf("an intent request carries %q", k)
		}
	}
	if sent[0]["lang"] != "ar" || strings.Contains(strings.Join(func() []string {
		var s []string
		for _, v := range sent[0] {
			s = append(s, v.(string))
		}
		return s
	}(), " "), "maktaba.example.home") {
		t.Fatalf("sent %v", sent[0])
	}
	// the purchase is looked for every 15 s from now on
	if !ents.Bursting() {
		t.Fatal("no burst after Buy")
	}
	if w := call(`{"plugin_id":"dev.example.free"}`); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not_for_sale") {
		t.Fatalf("free: %d %s", w.Code, w.Body)
	}
	// a buy page anywhere but the store's own is refused
	evil = true
	if w := call(`{"plugin_id":"dev.example.paid"}`); w.Code != http.StatusBadGateway {
		t.Fatalf("elsewhere: %d %s", w.Code, w.Body)
	}
	evil = false
	// offline: refused at the home
	n := len(paths)
	link.Offline = func() bool { return true }
	if w := call(`{"plugin_id":"dev.example.paid"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "store_offline") {
		t.Fatalf("offline: %d %s", w.Code, w.Body)
	}
	if len(paths) != n {
		t.Fatal("offline, the store got a request")
	}
}
