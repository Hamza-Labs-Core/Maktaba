package tvchange

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	core "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// as calls the API as p (nil: no one signed in).
func (w *world) as(p *principal.Principal, method, path, body string) (int, map[string]any) {
	w.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// The owner's rule of 2026-10-28: a manual change needs an admin's approval, enforced by
// the API (not by hiding the button): only an admin approves; nobody else, signed in or
// not, moves it. A manual change cannot skip its approval with "swap now" whatever its
// kind's rule (schedule: immediate); an automatic one never needs an approve call.
func TestApproveIsAdminOnly(t *testing.T) {
	w := newWorld(t)
	id := w.change(core.StateReady, core.SourceEditor, nil, "")
	w.exec(`UPDATE tv_changes SET kinds = '["schedule"]' WHERE id = $1`, id)
	member := &principal.Principal{UserID: "44444444-4444-4444-8444-444444444444"}
	for _, p := range []*principal.Principal{nil, member} {
		for _, action := range []string{"approve", "promote"} {
			if code, out := w.as(p, http.MethodPost, "/api/tv/changes/"+id+"/"+action, ""); code != http.StatusForbidden {
				t.Fatalf("%s by %+v: %d %v", action, p, code, out)
			}
		}
	}
	if w.state(id) != core.StateReady {
		t.Fatalf("moved by a non-admin: %s", w.state(id))
	}
	admin := &principal.Principal{UserID: "11111111-1111-4111-8111-111111111111", IsAdmin: true}
	if code, out := w.as(admin, http.MethodPost, "/api/tv/changes/"+id+"/promote", ""); code != http.StatusConflict {
		t.Fatalf("swap now without an approval: %d %v", code, out)
	}
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.state(id) != core.StateReady {
		t.Fatalf("a manual change went on without an approval: %s", w.state(id))
	}
	if code, out := w.as(admin, http.MethodPost, "/api/tv/changes/"+id+"/approve", ""); code != http.StatusOK ||
		out["state"] != core.StateApproved {
		t.Fatalf("an admin approves: %d %v", code, out)
	}

	// an automatic change: on the air by its rule, no approve call
	w.exec(`UPDATE tv_changes SET state = 'live' WHERE id = $1`, id) // out of the way: one in flight
	auto := w.change(core.StateReady, core.SourceWriteChannels, nil, "")
	w.exec(`UPDATE tv_changes SET kinds = '["schedule", "lineup"]', origin = 'auto' WHERE id = $1`, auto)
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.state(auto) != core.StateSwapping {
		t.Fatalf("an automatic change waits for an approve call: %s", w.state(auto))
	}
}
