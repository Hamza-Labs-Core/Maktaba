package cloudlink

import (
	"context"
	"io"
	"net/http"
	"time"
)

// LicenseHandler serves GET /admin/cloud-link/license on loopback: the
// home's API asks for the license its owner's plan grants (Epic 16 story
// 16.4) and the agent fetches it from the cloud with the server secret
// only it holds (GET /v1/servers/{id}/license), passing the cloud's
// status, body and Date header back as they came. The license is signed
// by the cloud and verified by the home, so a local caller learns
// nothing it could not read from the home's own Settings.
func LicenseHandler(rep *LinkReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			rep.CloudAPI+"/v1/servers/"+rep.Creds.ServerID+"/license", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.SetBasicAuth(rep.Creds.ServerID, rep.Creds.Secret)
		cl := rep.Client
		if cl == nil {
			cl = &http.Client{Timeout: 15 * time.Second}
		}
		resp, err := cl.Do(req)
		if err != nil {
			http.Error(w, "the cloud is unreachable", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if d := resp.Header.Get("Date"); d != "" {
			w.Header().Set("Date", d)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 64<<10))
	}
}
