package cloudlink

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// tvCodePath is a TV code's loopback route: the code, and "/claim" to claim it.
var tvCodePath = regexp.MustCompile(`^/admin/cloud-link/tv-codes/([A-Za-z0-9-]{1,16})(/claim)?$`)

// TVCodesHandler serves the cloud's TV codes to the home (Epic 58 story 58.03) on loopback, and
// passes each call to the cloud as the home (the server's secret, which only this agent holds):
//
//	GET  /admin/cloud-link/tv-codes/{code}        -> GET  /v1/homes/{id}/tv-codes/{code}
//	POST /admin/cloud-link/tv-codes/{code}/claim  -> POST /v1/homes/{id}/tv-codes/{code}/claim
//
// The cloud's answer comes back as it is; an unreachable cloud is a 502.
func TVCodesHandler(rep *LinkReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := tvCodePath.FindStringSubmatch(r.URL.Path)
		if m == nil || (m[2] == "" && r.Method != http.MethodGet) || (m[2] != "" && r.Method != http.MethodPost) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var body io.Reader
		if r.Method == http.MethodPost {
			body = http.MaxBytesReader(w, r.Body, 4<<10)
		}
		u := strings.TrimRight(rep.CloudAPI, "/") + "/v1/homes/" + rep.Creds.ServerID + "/tv-codes/" + m[1] + m[2]
		req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.SetBasicAuth(rep.Creds.ServerID, rep.Creds.Secret)
		if r.Method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		cl := rep.Client
		if cl == nil {
			cl = &http.Client{Timeout: 15 * time.Second}
		}
		resp, err := cl.Do(req)
		if err != nil {
			http.Error(w, "the cloud is out of reach: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 64<<10))
	}
}
