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

// Story 54.18 §3, §4 (the home's half): a rating and a report go to the store signed by the
// home, with the plugin's id and version and nothing else; an unlinked home is told.
func TestRateAndReportGoToTheStoreSigned(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if _, err := db.Exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest, review_level)
		VALUES ('dev.example.r', '1.2.0', 'd', '/x', '{"capabilities":{"required":[],"optional":[]},"strings":{"name":"n","summary":"s"}}', 'reviewed')`); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	var paths, auths []string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got, paths, auths = append(got, m), append(paths, r.URL.Path), append(auths, r.Header.Get("Authorization"))
		if strings.HasSuffix(r.URL.Path, "/ratings") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"rating_too_early","after_hours":24}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"filed"}`))
	}))
	defer store.Close()
	link := &storelink.Link{DB: db, Secrets: secrets.NewMemory(), StoreURL: store.URL}
	h := &Admin{L: &pl.Lifecycle{DB: db}, Store: link}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a1", IsAdmin: true}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := call("/api/plugins/dev.example.r/report", `{"reason":"malware"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "store_not_linked") {
		t.Fatalf("unlinked: %d %s", w.Code, w.Body)
	}
	// linked: a key in the secrets store and the link's state
	if _, err := link.Secrets.(*secrets.Memory).Get(context.Background(), storelink.KeyName); err == nil {
		t.Fatal("a key before linking")
	}
	_ = link.Secrets.Put(context.Background(), storelink.KeyName, bytes.Repeat([]byte{7}, 32), "test")
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, '{"home_id":"h_1","linked_at":"2026-11-06T00:00:00Z"}')`, storelink.StateKey); err != nil {
		t.Fatal(err)
	}
	if w := call("/api/plugins/dev.example.r/report", `{"reason":"malware","details":"mines"}`); w.Code != http.StatusOK {
		t.Fatalf("report: %d %s", w.Code, w.Body)
	}
	if w := call("/api/plugins/dev.example.r/rate", `{"stars":4}`); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "rating_too_early") {
		t.Fatalf("rate early: %d %s", w.Code, w.Body)
	}
	if len(got) != 2 || paths[0] != "/v1/homes/h_1/reports" || got[0]["plugin"] != "dev.example.r" || got[0]["version"] != "1.2.0" {
		t.Fatalf("sent %v %v", paths, got)
	}
	for k := range got[0] {
		if k != "plugin" && k != "version" && k != "reason" && k != "details" {
			t.Fatalf("a report carries %q", k)
		}
	}
	if !strings.HasPrefix(auths[0], "MKS-Home id=h_1, ") {
		t.Fatalf("not signed: %q", auths[0])
	}
	// offline (MAKTABA_OFFLINE): refused at the home, the store gets nothing
	link.Offline = func() bool { return true }
	for _, c := range [][2]string{{"/api/plugins/dev.example.r/rate", `{"stars":5}`}, {"/api/plugins/dev.example.r/report", `{"reason":"malware"}`}} {
		if w := call(c[0], c[1]); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "store_offline") {
			t.Fatalf("%s offline: %d %s", c[0], w.Code, w.Body)
		}
	}
	if len(got) != 2 {
		t.Fatalf("offline, the store got %d more request(s)", len(got)-2)
	}
}
