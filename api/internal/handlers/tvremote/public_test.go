package tvremote

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Epic 34 over HTTP: admin only (the API is the gate, not the page), the
// rights confirmation, the settings' URL check, the site secrets shown once.
func TestPublicRoutes(t *testing.T) {
	h, f, _, _ := remoteServer(t)
	admin := &principal.Principal{UserID: "admin-1", IsAdmin: true}
	viewer := &principal.Principal{UserID: "u1"}
	routes := [][2]string{{"GET", "/api/tv/public"}, {"PUT", "/api/tv/public"},
		{"PUT", "/api/tv/public/channels/" + chID}, {"POST", "/api/tv/public/site-secrets"}}
	for _, rt := range routes {
		if rec := as(h, rt[0], rt[1], `{}`, viewer); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a viewer: %d", rt[0], rt[1], rec.Code)
		}
		if rec := as(h, rt[0], rt[1], `{}`, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s signed out: %d", rt[0], rt[1], rec.Code)
		}
	}
	rec := as(h, "PUT", "/api/tv/public/channels/"+chID, `{"public":true}`, admin)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"code":"rights_confirmation_required"`) {
		t.Fatalf("without rights: %d %s", rec.Code, rec.Body)
	}
	rec = as(h, "PUT", "/api/tv/public/channels/"+chID, `{"public":true,"rights_confirmed":true,"slug":"one-tv"}`, admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"rights_confirmed_by":"admin-1"`) {
		t.Fatalf("with rights: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "PUT", "/api/tv/public", `{"enabled":true,"site_url":"http://tv.example.com"}`, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("plain http site: %d %s", rec.Code, rec.Body)
	}
	rec = as(h, "PUT", "/api/tv/public", `{"enabled":true,"site_url":"https://tv.example.com/"}`, admin)
	var v PublicView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || !v.Settings.Enabled || v.Settings.SiteURL != "https://tv.example.com" || v.SecretsSet ||
		len(v.Channels) != 1 || !v.Channels[0].Public || v.Channels[0].Slug != "one-tv" {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	rec = as(h, "POST", "/api/tv/public/site-secrets", ``, admin)
	var s struct {
		HomeHMAC      string `json:"home_hmac"`
		SigningKeyID  string `json:"signing_key_id"`
		SigningKeyPEM string `json:"signing_key_pem"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if rec.Code != 200 || len(s.HomeHMAC) != 64 || s.SigningKeyPEM == "" || f.Keys[s.SigningKeyID].ID == "" {
		t.Fatalf("site secrets: %d %s", rec.Code, rec.Body)
	}
	rec = as(h, "GET", "/api/tv/public", ``, admin)
	if !strings.Contains(rec.Body.String(), `"secrets_set":true`) || strings.Contains(rec.Body.String(), s.HomeHMAC) ||
		strings.Contains(rec.Body.String(), "PRIVATE") {
		t.Errorf("status after secrets (never answered again): %s", rec.Body)
	}
}
