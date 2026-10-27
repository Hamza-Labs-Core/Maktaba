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

// storageSupervisor answers /config/storage as maktaba-server does.
func storageFixture(t *testing.T) (http.Handler, *atomic.Int32, *[]string, *Handler) {
	t.Helper()
	calls := &atomic.Int32{}
	bodies := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		b, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		var in struct{ Target string }
		_ = json.Unmarshal(b, &in)
		switch r.Method + " " + r.URL.Path {
		case "GET /config/storage":
			_, _ = io.WriteString(w, `{"data_dir":"/d","folders":[],"disks":[],"move":{"state":"none"},"can_move":true}`)
		case "GET /config/storage/disks":
			_, _ = io.WriteString(w, `{"disks":[{"mount":"/Volumes/Big"}]}`)
		case "POST /config/storage/check":
			_, _ = io.WriteString(w, `{"ok":true,"target":"`+in.Target+`","problems":[],"warnings":[]}`)
		case "POST /config/storage/move":
			if in.Target == "bad" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"ok":false,"problems":[{"code":"target_relative","message":"whole path"}]}`)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"state":"requested","target":"`+in.Target+`"}`)
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

var storageRoutes = []struct{ method, path, body string }{
	{"GET", "/api/server/storage", ""},
	{"GET", "/api/server/storage/disks", ""},
	{"POST", "/api/server/storage/check", `{"target":"/Volumes/Big/Maktaba"}`},
	{"POST", "/api/server/storage/move", `{"target":"/Volumes/Big/Maktaba"}`},
}

// TestStorageRefusesNonAdmins: the API enforces the role. An anonymous caller and
// a viewer get 403 on every storage route before the supervisor is called.
func TestStorageRefusesNonAdmins(t *testing.T) {
	r, calls, _, _ := storageFixture(t)
	viewer := &principal.Principal{UserID: "u2", Libraries: []string{"lib"}}
	for _, c := range storageRoutes {
		for _, p := range []*principal.Principal{nil, viewer} {
			if rec := call(r, p, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v: %d, want 403", c.method, c.path, p, rec.Code)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("the supervisor was called %d times", n)
	}
}

func TestStorageForAnAdmin(t *testing.T) {
	r, _, bodies, h := storageFixture(t)
	rec := call(r, admin, "GET", "/api/server/storage", "")
	var doc map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["available"] != true || doc["data_dir"] != "/d" {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	if rec := call(r, admin, "GET", "/api/server/storage/disks", ""); rec.Code != 200 {
		t.Errorf("disks %d", rec.Code)
	}
	if rec := call(r, admin, "POST", "/api/server/storage/check", `{"target":"/x"}`); rec.Code != 200 {
		t.Errorf("check %d", rec.Code)
	}
	if rec := call(r, admin, "POST", "/api/server/storage/move", `{"target":"bad"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a refused move %d %s", rec.Code, rec.Body)
	}
	rec = call(r, admin, "POST", "/api/server/storage/move", `{"target":"/Volumes/Big/Maktaba"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("move %d %s", rec.Code, rec.Body)
	}
	last := (*bodies)[len(*bodies)-1]
	if last != `{"by":"the web (user `+adminID+`)","target":"/Volumes/Big/Maktaba"}` {
		t.Errorf("the supervisor got %s", last)
	}
	var n int
	if err := h.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, ActionStorageMoveRequested).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit rows %d %v", n, err)
	}
	if rec := call(r, admin, "POST", "/api/server/storage/move", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("not json %d", rec.Code)
	}
}

func TestStorageWithoutASupervisor(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Mount(r)
	rec := call(r, admin, "GET", "/api/server/storage", "")
	if rec.Code != 200 || !json.Valid(rec.Body.Bytes()) || rec.Body.String() == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var doc map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc["available"] != false {
		t.Errorf("doc %v", doc)
	}
	if rec := call(r, admin, "POST", "/api/server/storage/move", `{"target":"/x"}`); rec.Code != http.StatusConflict {
		t.Errorf("move %d", rec.Code)
	}
}
