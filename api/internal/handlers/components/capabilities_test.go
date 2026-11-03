package components

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// capsRouter serves GET /api/capabilities over a home whose search.semantic is off.
func capsRouter(t *testing.T) (chi.Router, *svc.Capabilities) {
	t.Helper()
	caps := svc.NewCapabilities(svc.CapabilitiesOptions{})
	d := map[string]store.Desired{}
	for _, c := range components.Default().Components() {
		if c.Switchable {
			d[c.ID] = store.Desired{ID: c.ID, Want: components.WantEnabled, Generation: 1}
		}
	}
	d["livetv.inprocess"] = store.Desired{ID: "livetv.inprocess", Want: components.WantInstalled, Generation: 1}
	d["search.semantic"] = store.Desired{ID: "search.semantic", Want: components.WantInstalled, Generation: 2}
	caps.Apply(d, 1842)
	r := chi.NewRouter()
	(&Capabilities{Caps: caps}).Mount(r)
	return r, caps
}

func getCaps(r chi.Router, p *principal.Principal, inm string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/capabilities", nil)
	if inm != "" {
		req.Header.Set("If-None-Match", inm)
	}
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

type capsBody struct {
	ETag     string            `json:"etag"`
	Features map[string]string `json:"features"`
	Admin    *struct {
		Manage bool                        `json:"manage_components"`
		Off    map[string]svc.FeatureOffBy `json:"off"`
	} `json:"admin"`
}

// T04 + AC1/AC2: no one 401; a viewer, a cloud play sign-in (even an admin's), a token
// without the admin scope and a machine get the features only; an admin and an admin's
// manage link also get the admin block naming search.semantic and switched_off.
func TestCapabilitiesRoles(t *testing.T) {
	r, _ := capsRouter(t)
	code(t, getCaps(r, nil, ""), http.StatusUnauthorized, "auth_required")
	for name, p := range map[string]*principal.Principal{"viewer": viewer(), "play": playAdmin(), "token": token(), "machine": machine()} {
		rec := getCaps(r, p, "")
		var b capsBody
		decode(t, rec, http.StatusOK, &b)
		if b.Admin != nil || b.Features["semantic_search"] != "off" || b.ETag != "c-1842-1" {
			t.Fatalf("%s: %s", name, rec.Body)
		}
		var raw map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &raw)
		if len(raw) != 2 {
			t.Fatalf("%s: internals in the viewer's answer: %s", name, rec.Body)
		}
	}
	for name, p := range map[string]*principal.Principal{"admin": admin(), "manage": manageLink()} {
		var b capsBody
		decode(t, getCaps(r, p, ""), http.StatusOK, &b)
		if b.Admin == nil || !b.Admin.Manage {
			t.Fatalf("%s: no admin block", name)
		}
		off := b.Admin.Off["semantic_search"]
		if len(off.Components) != 1 || off.Components[0] != "search.semantic" || off.Reason != "switched_off" {
			t.Fatalf("%s: off %+v", name, b.Admin.Off)
		}
	}
}

// T05: the ETag header, Cache-Control, and If-None-Match: the same is 304 with no body,
// an old one 200.
func TestCapabilitiesETag(t *testing.T) {
	r, caps := capsRouter(t)
	rec := getCaps(r, viewer(), "")
	if rec.Header().Get("ETag") != `"c-1842-1"` || rec.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("headers %v", rec.Header())
	}
	for _, inm := range []string{`"c-1842-1"`, `W/"c-1842-1"`, `"c-1-1", "c-1842-1"`, "*"} {
		if rec := getCaps(r, viewer(), inm); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Fatalf("If-None-Match %s: %d %q", inm, rec.Code, rec.Body)
		}
	}
	if rec := getCaps(r, viewer(), `"c-1841-1"`); rec.Code != http.StatusOK {
		t.Fatalf("old etag: %d", rec.Code)
	}
	caps.Apply(nil, 1843)
	if rec := getCaps(r, viewer(), `"c-1842-1"`); rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"c-1843-1"` {
		t.Fatalf("after a change: %d %s", rec.Code, rec.Header().Get("ETag"))
	}
}
