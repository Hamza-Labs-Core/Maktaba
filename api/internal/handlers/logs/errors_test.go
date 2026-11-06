package logs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// Epic 53 story 53.10 T11: the error group routes, as nobody (401), a viewer (403) and an
// admin (the role's answer; a group's fingerprint in its path).
func TestErrorRoutesRoles(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	role := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"groups":[]}`)
	}))
	defer role.Close()
	h := &Handler{IndexAddr: strings.TrimPrefix(role.URL, "http://")}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(method, path string, p *principal.Principal) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"status":"resolved","version":"0.3.80"}`))
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	routes := [][2]string{{"GET", "/api/admin/errors?status=new"}, {"GET", "/api/admin/errors/ab12"},
		{"POST", "/api/admin/errors/ab12/status"}}
	for _, rt := range routes {
		if c := call(rt[0], rt[1], nil); c != http.StatusUnauthorized {
			t.Errorf("%v as nobody: %d", rt, c)
		}
		if c := call(rt[0], rt[1], &principal.Principal{UserID: "v"}); c != http.StatusForbidden {
			t.Errorf("%v as a viewer: %d", rt, c)
		}
	}
	admin := &principal.Principal{UserID: "a", IsAdmin: true}
	for _, rt := range routes {
		if c := call(rt[0], rt[1], admin); c >= 300 {
			t.Errorf("%v as an admin: %d", rt, c)
		}
	}
	want := "GET /errors?status=new,GET /errors/ab12?,POST /errors/ab12/status?"
	if got := strings.Join(seen, ","); got != want {
		t.Errorf("the role saw %q, want %q", got, want)
	}
	h.IndexAddr = "127.0.0.1:1"
	if c := call("POST", "/api/admin/errors/ab12/status", admin); c != http.StatusServiceUnavailable {
		t.Errorf("no role: %d", c)
	}
}

type notices struct {
	mu  sync.Mutex
	got []notify.Payload
}

func (n *notices) Notify(_ context.Context, _ string, p notify.Payload) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, p)
	return nil
}

// T12: the loop turns the role's notices into admin notifications: back ones say so, the
// last says how many more; no role, nothing.
func TestErrorNotifier(t *testing.T) {
	role := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/errors/notices" || r.URL.Query().Get("limit") != "10" {
			t.Errorf("asked %s %s", r.Method, r.URL)
		}
		_, _ = io.WriteString(w, `{"notices":[{"fp":"a1","title":"db.timeout: query","service":"api"},
			{"fp":"b2","title":"boom","service":"api","came_back_in":"0.3.81"}],"more":4}`)
	}))
	defer role.Close()
	n := &notices{}
	e := &ErrorNotifier{Addr: func() string { return strings.TrimPrefix(role.URL, "http://") }, Notifier: n}
	e.Once(context.Background())
	if len(n.got) != 2 || n.got[0].TitleKey != "notify.logs.error_group" || n.got[1].TitleKey != "notify.logs.error_group_back_more" ||
		n.got[1].Params["more"] != 4 || n.got[0].Link != "/admin/errors/a1" || n.got[0].Subject != "error_group:a1" {
		t.Fatalf("notices: %+v", n.got)
	}
	e.Addr = func() string { return "" }
	e.Once(context.Background())
	if len(n.got) != 2 {
		t.Errorf("no role: nothing")
	}
}
