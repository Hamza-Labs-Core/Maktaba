package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// T12: on the running server's database, `components list --json` answers what
// GET /api/components answers, and `components stop youtube --yes` sets the hold, audited
// as cli; then `disable` writes the same row and audit the web's PUT would.
func TestComponentsCLIOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	s := components.NewService(db)
	r := chi.NewRouter()
	(&componentsh.Handler{Service: s, DB: db}).Mount(r)
	get := func() map[string]any {
		req := httptest.NewRequest("GET", "/api/components", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u1", IsAdmin: true}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body)
		}
		var v map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	c, out, _ := cli(nil, false, "")
	c.svc = s
	if code := runCLI(c, "list", "--json"); code != 0 {
		t.Fatalf("list exit %d", code)
	}
	var fromCLI map[string]any
	if err := json.Unmarshal(out.Bytes(), &fromCLI); err != nil {
		t.Fatal(err)
	}
	if web := get(); !reflect.DeepEqual(fromCLI, web) {
		t.Fatalf("the CLI's list differs from the web's")
	}

	c, _, _ = cli(nil, false, "")
	c.svc = s
	if code := runCLI(c, "stop", "youtube", "--yes"); code != 0 {
		t.Fatalf("stop exit %d", code)
	}
	d, err := s.Store.DesiredOf(context.Background(), "youtube")
	if err != nil || d.Hold == nil || d.Hold.By != "maya" {
		t.Fatalf("youtube %+v %v", d, err)
	}
	if code := runCLI(c, "disable", "youtube", "--yes"); code != 0 {
		t.Fatalf("disable exit %d", code)
	}
	var action, source, payload string
	if err := db.QueryRow(`SELECT action, actor_source, payload::text FROM audit_log WHERE category = 'components'
		AND target_id = 'youtube' ORDER BY id DESC LIMIT 1`).Scan(&action, &source, &payload); err != nil {
		t.Fatal(err)
	}
	if action != "component.disable" || source != "cli" {
		t.Fatalf("audit %s %s %s", action, source, payload)
	}
	if d, _ := s.Store.DesiredOf(context.Background(), "youtube"); d.Want != catalog.WantInstalled || d.Hold == nil {
		t.Fatalf("youtube after disable %+v", d)
	}
}

// The CLI's preview shows the phase a component has now on the left of the arrow (what
// `components list` says), not the one the desired state alone gives it: Smart search
// really waits, so turning it off reads waiting -> off.
func TestComponentsCLIPreviewFromIsActual(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	s := components.NewService(db)
	if err := s.Store.ReportStatus(context.Background(), store.Home, "supervisor", []store.Status{{ID: "search.semantic",
		Phase: catalog.PhaseWaiting, Conditions: []store.Condition{
			{Type: catalog.CondRequirementsMet, Status: "unknown", Params: map[string]any{"component": "runtime.ml"}}}}}); err != nil {
		t.Fatal(err)
	}
	c, out, _ := cli(nil, false, "")
	c.svc = s
	if code := runCLI(c, "disable", "search.semantic"); code != exitComponentsNeedYes {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out.String(), "search.semantic: waiting -> off") {
		t.Fatalf("preview: %s", out)
	}
}
