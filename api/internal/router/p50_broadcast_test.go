package router

import (
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 50.25: a claimed home honours the revocation list once it holds a broadcast signing key
// (the release's or MAKTABA_BROADCAST_PUBKEYS); with none, or an unreadable set, it does not.
func TestMountBroadcastWiresTheRevocationGuard(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	keys := `{"k1":"` + base64.RawURLEncoding.EncodeToString(pub) + `"}`
	svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true, RevocationKeys: keys})
	if !svc.Revocations.Enabled() {
		t.Fatal("a claimed home with a key does not honour the list")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true}); svc.Revocations.Enabled() {
		t.Fatal("a home with no key honours a list it can't check")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true, RevocationKeys: "{"}); svc.Revocations.Enabled() {
		t.Fatal("an unreadable key set is honoured")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{NoLoop: true, RevocationKeys: keys}); svc.Revocations.Enabled() {
		t.Fatal("an unclaimed home has a revocation source")
	}
}

// Story 50.24: a claimed home asks Maktaba Cloud for its takedowns and records through the same
// agent; an unclaimed one has nothing to ask.
func TestMountBroadcastWiresTheModerationCloud(t *testing.T) {
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{AgentAddr: "127.0.0.1:1", NoLoop: true}); svc.Moderation == nil {
		t.Fatal("a claimed home has no moderation cloud")
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{NoLoop: true}); svc.Moderation != nil {
		t.Fatal("an unclaimed home asks for takedowns")
	}
}

// Story 50.28: a claimed home asks the relay agent for its viewer report (the agent's
// home/analytics route, period and zone in the query) and answers its admins with it; a viewer is
// refused before the agent is asked; an unclaimed home has nothing to ask.
func TestMountBroadcastWiresTheViewerReport(t *testing.T) {
	var asked []string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = io.WriteString(w, `{"level":"hours","live_viewers":2,"hours":{"today":1,"week":2,"month":3,"allowance":50}}`)
	}))
	defer agent.Close()
	r := chi.NewRouter()
	svc := MountBroadcast(r, BroadcastDeps{AgentAddr: strings.TrimPrefix(agent.URL, "http://"), NoLoop: true})
	if svc.Analytics == nil {
		t.Fatal("a claimed home has no viewer report source")
	}
	get := func(p *principal.Principal) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, as(httptest.NewRequest("GET", "/api/tv/broadcast/analytics?period=month&tz=UTC", nil), p))
		return rec
	}
	if rec := get(viewerP()); rec.Code != http.StatusForbidden || len(asked) != 0 {
		t.Fatalf("a viewer: %d, agent asked %q", rec.Code, asked)
	}
	rec := get(adminP())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cloud":"ok"`) ||
		!strings.Contains(rec.Body.String(), `"live_viewers":2`) {
		t.Fatalf("an admin: %d %s", rec.Code, rec.Body)
	}
	if len(asked) != 1 || asked[0] != "GET /admin/cloud-link/broadcast/home/analytics?period=month&tz=UTC" {
		t.Fatalf("the agent was asked %q", asked)
	}
	if svc := MountBroadcast(chi.NewRouter(), BroadcastDeps{NoLoop: true}); svc.Analytics != nil {
		t.Fatal("an unclaimed home asks for a viewer report")
	}
}
