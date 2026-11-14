package auth

// Epic 58 story 58.03: a person's app sign-ins (a TV signed in through a code, the home's or a
// cloud code's), each signed out from Profile.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/refresh"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
)

func TestAppSignInsAreTheCallersPairedApps(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	h := NewHandler(Deps{DB: db, Keys: testKeys(t)})
	h.Users.Params, h.RefreshTokens.Params = lightHash(), lightHash()
	ctx := context.Background()
	mk := func(name string) string {
		u, err := h.Users.Create(ctx, users.CreateInput{Username: name, Password: "correct horse battery"})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	ana, ben := mk("ana"), mk("ben")
	issue := func(uid string, meta map[string]any) string {
		tok, err := h.RefreshTokens.Issue(ctx, refresh.IssueInput{UserID: uid, ClientMeta: meta})
		if err != nil {
			t.Fatal(err)
		}
		return tok.FamilyID
	}
	tv := issue(ana, map[string]any{"origin": "pairing", "device_kind": "tv", "device_label": "tizen", "via": "cloud_code"})
	phone := issue(ana, map[string]any{"origin": "pairing", "device_kind": "phone", "device_label": "Pixel"})
	_ = issue(ana, map[string]any{"origin": "password"}) // a browser sign-in is a session, not an app
	bens := issue(ben, map[string]any{"origin": "pairing", "device_kind": "tv", "device_label": "webos"})

	r := chi.NewRouter()
	r.Get("/api/me/app-sign-ins", h.MeAppSignIns)
	r.Delete("/api/me/app-sign-ins/{id}", h.RevokeMeAppSignIn)
	as := func(uid, fam, method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if uid != "" {
			req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: uid, RefreshFamily: fam}))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	list := func(uid, fam string) map[string]map[string]any {
		rec := as(uid, fam, http.MethodGet, "/api/me/app-sign-ins")
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var body struct{ Items []map[string]any }
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := map[string]map[string]any{}
		for _, it := range body.Items {
			out[it["id"].(string)] = it
		}
		return out
	}

	got := list(ana, phone)
	if len(got) != 2 || got[tv]["via"] != "cloud_code" || got[tv]["label"] != "tizen" || got[tv]["kind"] != "tv" || got[tv]["current"] != false || got[phone]["current"] != true {
		t.Fatalf("ana's apps: %v", got)
	}
	if rec := as("", "", http.MethodGet, "/api/me/app-sign-ins"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no one: %d", rec.Code)
	}
	// someone else's sign-in, or no sign-in at all, is not found
	for _, id := range []string{bens, "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		if rec := as(ana, "", http.MethodDelete, "/api/me/app-sign-ins/"+id); rec.Code != http.StatusNotFound {
			t.Fatalf("revoke %s: %d", id, rec.Code)
		}
	}
	if len(list(ben, "")) != 1 {
		t.Fatal("ana signed ben's TV out")
	}
	if rec := as(ana, "", http.MethodDelete, "/api/me/app-sign-ins/"+tv); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if got := list(ana, ""); len(got) != 1 || got[phone] == nil {
		t.Fatalf("after signing the TV out: %v", got)
	}
}

// The refresh path knows a cloud code's TV by the same word the pairing handler writes.
func TestViaCloudCodeIsThePairingHandlers(t *testing.T) {
	if viaCloudCode != discoveryh.ViaCloudCode {
		t.Fatalf("%q != %q", viaCloudCode, discoveryh.ViaCloudCode)
	}
}
