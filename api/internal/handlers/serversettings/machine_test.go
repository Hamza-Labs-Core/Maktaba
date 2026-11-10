package serversettings

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// machineFixture answers /config/machine as maktaba-server does.
func machineFixture(t *testing.T) (http.Handler, *atomic.Int32, *[]string, *Handler) {
	t.Helper()
	calls := &atomic.Int32{}
	bodies := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		var in struct{ Tier string }
		_ = json.Unmarshal(b, &in)
		switch r.Method + " " + r.URL.Path {
		case "GET /config/machine":
			_, _ = io.WriteString(w, `{"tier":"standard","detected":"standard","saved":"auto","reason":{"tier":"standard","memory_gb":8,"gpu":""}}`)
		case "PUT /config/machine":
			if in.Tier == "huge" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"errors":{"tier":"light, standard, strong or auto"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"tier":"standard","saved":"`+in.Tier+`","restart_needed":true}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	tf := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tf, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Supervisor: &Supervisor{URL: srv.URL, TokenFile: tf}, DB: testDB(t)}
	r := chi.NewRouter()
	h.Mount(r)
	return r, calls, bodies, h
}

// Epic 59 story 59.04: the API enforces the role. An anonymous caller and a viewer get 403
// on both machine routes before the supervisor is called.
func TestMachineRefusesNonAdmins(t *testing.T) {
	r, calls, _, _ := machineFixture(t)
	viewer := &principal.Principal{UserID: "u2", Libraries: []string{"lib"}}
	for _, c := range []struct{ method, body string }{{"GET", ""}, {"PUT", `{"tier":"light"}`}} {
		for _, p := range []*principal.Principal{nil, viewer} {
			if rec := call(r, p, c.method, "/api/server/machine", c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s as %v: %d, want 403", c.method, p, rec.Code)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the supervisor was called %d times", n)
	}
}

func TestMachineForAnAdmin(t *testing.T) {
	r, _, bodies, h := machineFixture(t)
	rec := call(r, admin, "GET", "/api/server/machine", "")
	var doc map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["available"] != true || doc["tier"] != "standard" {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	if rec := call(r, admin, "PUT", "/api/server/machine", `{"tier":"huge"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a non-tier: %d %s", rec.Code, rec.Body)
	}
	rec = call(r, admin, "PUT", "/api/server/machine", `{"tier":"light","extra":"x"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["saved"] != "light" {
		t.Fatalf("PUT %d %s", rec.Code, rec.Body)
	}
	if last := (*bodies)[len(*bodies)-1]; last != `{"tier":"light"}` {
		t.Errorf("the supervisor got %s", last)
	}
	var n int
	if err := h.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, ActionTierChanged).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit rows %d %v (a refused pick is not audited)", n, err)
	}
	if rec := call(r, admin, "PUT", "/api/server/machine", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("not json %d", rec.Code)
	}
}

func TestMachineWithoutASupervisor(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Mount(r)
	rec := call(r, admin, "GET", "/api/server/machine", "")
	var doc map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["available"] != false {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := call(r, admin, "PUT", "/api/server/machine", `{"tier":"light"}`); rec.Code != http.StatusConflict {
		t.Errorf("PUT %d", rec.Code)
	}
}
