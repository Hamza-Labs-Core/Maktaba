package cloudlink

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The agent passes the cloud's answer through with the server's own
// credentials, status and Date intact.
func TestLicenseHandlerPassesThrough(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "srv-1" || secret != "s3cret" || r.URL.Path != "/v1/servers/srv-1/license" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Date", "Mon, 26 Oct 2026 10:00:00 GMT")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"license_revoked"}`)
	}))
	defer cloud.Close()
	h := LicenseHandler(&LinkReporter{CloudAPI: cloud.URL, Creds: Credentials{ServerID: "srv-1", Secret: "s3cret"}})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/cloud-link/license", nil))
	if rec.Code != http.StatusForbidden || rec.Body.String() != `{"error":"license_revoked"}` ||
		rec.Header().Get("Date") != "Mon, 26 Oct 2026 10:00:00 GMT" {
		t.Fatalf("%d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Date"))
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/cloud-link/license", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
}
