//go:build integration

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
)

// Epic 16 story 16.4 on the api role's real router: a paid plan becomes
// a license bound to the home, signed with the published key; the free
// plan grants none; a suspended account and a revoked id lock; the
// revocation list is public and signed; the admin routes need the
// service-admin role.
func TestPlanLicenseOnTheRealRouter(t *testing.T) {
	e := newEnv(t)
	owner := e.registerVerified("lic-owner@example.test")
	uid := e.userID("lic-owner@example.test")
	code := owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(t)["code"].(string)
	agent := e.client()
	red := agent.mustStatus(agent.do("POST", "/v1/servers/claims/redeem",
		map[string]string{"code": code, "name": "Home", "slug": "lichome"}), http.StatusCreated).json(t)
	id, secret := red["server_id"].(string), red["server_secret"].(string)

	key := e.client().do("GET", "/.well-known/maktaba-entitlement-key", nil).json(t)
	rawPub, _ := base64.StdEncoding.DecodeString(key["public_key"].(string))
	pub := ed25519.PublicKey(rawPub)

	fetch := func(secret string) (int, map[string]json.RawMessage) {
		req := httptest.NewRequest("GET", "/v1/servers/"+id+"/license", nil)
		req.RemoteAddr = "198.51.100.251:40000"
		req.SetBasicAuth(id, secret)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		var body map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	if got, _ := fetch("wrong"); got != http.StatusUnauthorized {
		t.Fatalf("license with a wrong secret: %d", got)
	}
	if got, body := fetch(secret); got != http.StatusNotFound || !strings.Contains(string(body["error"]), "no_license") {
		t.Fatalf("free plan: %d %s", got, body["error"])
	}

	e.exec(`UPDATE users SET plan = 'family' WHERE id = $1`, uid)
	got, body := fetch(secret)
	if got != http.StatusOK {
		t.Fatalf("paid plan: %d", got)
	}
	var lic license.License
	if err := json.Unmarshal(body["license"], &lic); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(lic.License)
	sig, _ := base64.StdEncoding.DecodeString(lic.Signature)
	if !ed25519.Verify(pub, payload, sig) {
		t.Fatal("the license does not verify with the published key")
	}
	if lic.License.Tier != license.TierPro || lic.License.ServerID != id || lic.License.LicenseID != "cloud-"+id ||
		lic.License.Holder != "lic-owner@example.test" || time.Until(lic.License.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("license: %+v", lic.License)
	}
	if !strings.Contains(string(body["manage_url"]), "/billing") {
		t.Fatalf("manage_url: %s", body["manage_url"])
	}
	if e.count(`SELECT count(*) FROM licenses_issued WHERE license_id = $1 AND kind = 'plan'`, "cloud-"+id) != 1 {
		t.Fatal("issue not recorded")
	}

	// a suspended account (a disputed payment) locks
	e.exec(`UPDATE users SET status = 'suspended' WHERE id = $1`, uid)
	if got, body := fetch(secret); got != http.StatusForbidden || !strings.Contains(string(body["error"]), "license_revoked") {
		t.Fatalf("suspended: %d %s", got, body["error"])
	}
	e.exec(`UPDATE users SET status = 'active' WHERE id = $1`, uid)

	// the admin routes need the role
	if got := owner.do("POST", "/v1/admin/licenses/cloud-"+id+"/revoke", map[string]string{"reason": "x"}); got.code != http.StatusForbidden {
		t.Fatalf("an account revoked a license: %d", got.code)
	}
	if got := agent.do("GET", "/v1/admin/licenses", nil); got.code != http.StatusUnauthorized {
		t.Fatalf("no one listed licenses: %d", got.code)
	}
	ops := e.serviceAdmin("lic-ops@example.test")
	ops.mustStatus(ops.do("POST", "/v1/admin/licenses/cloud-"+id+"/revoke", map[string]string{"reason": "fraud"}), http.StatusNoContent)
	if got, _ := fetch(secret); got != http.StatusForbidden {
		t.Fatalf("revoked id still issued: %d", got)
	}
	list := e.client().mustStatus(e.client().do("GET", "/v1/licenses/revocations", nil), http.StatusOK)
	var rl license.RevocationList
	if err := json.Unmarshal(list.body, &rl); err != nil {
		t.Fatal(err)
	}
	rp, _ := json.Marshal(rl.Revocations)
	rsig, _ := base64.StdEncoding.DecodeString(rl.Signature)
	if !ed25519.Verify(pub, rp, rsig) || len(rl.Revocations.LicenseIDs) != 1 || rl.Revocations.LicenseIDs[0] != "cloud-"+id {
		t.Fatalf("revocation list: %s", list.body)
	}
	listed := ops.mustStatus(ops.do("GET", "/v1/admin/licenses", nil), http.StatusOK).json(t)
	if listed["total"].(float64) != 1 {
		t.Fatalf("admin list: %v", listed)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.license.revoke' AND target_id = $1`, "cloud-"+id) != 1 {
		t.Fatal("revocation not audited")
	}
	ops.mustStatus(ops.do("POST", "/v1/admin/licenses/cloud-"+id+"/unrevoke", map[string]string{"reason": "resolved"}), http.StatusNoContent)
	if got, _ := fetch(secret); got != http.StatusOK {
		t.Fatalf("unrevoked: %d", got)
	}
}
