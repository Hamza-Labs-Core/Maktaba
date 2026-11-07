package cloudlink

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxReportBody is the largest report the agent passes on (the cloud's limit).
const MaxReportBody = 8 << 20

// ReportsHandler serves the home's problem reports to Maktaba (Epic 53 story 53.16) on
// loopback, and passes each to the cloud as the home (the server's secret, which only this
// agent holds):
//
//	POST   /admin/cloud-link/reports          a report (multipart) -> POST /v1/homes/{id}/reports
//	GET    /admin/cloud-link/reports/replies  ?since= -> the service admin's answers
//	DELETE /admin/cloud-link/reports          every report of the home, at once
//
// The cloud's answer comes back as it is (a 503 while Maktaba takes none); an unreachable
// cloud is a 502, and the home keeps the report in its outbox.
func ReportsHandler(rep *LinkReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := "/v1/homes/" + rep.Creds.ServerID + "/reports"
		switch {
		case r.URL.Path == "/admin/cloud-link/reports" && (r.Method == http.MethodPost || r.Method == http.MethodDelete):
		case r.URL.Path == "/admin/cloud-link/reports/replies" && r.Method == http.MethodGet:
			path += "/replies"
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		var body io.Reader
		if r.Method == http.MethodPost {
			body = http.MaxBytesReader(w, r.Body, MaxReportBody)
		}
		u := strings.TrimRight(rep.CloudAPI, "/") + path
		if r.URL.RawQuery != "" {
			u += "?" + r.URL.RawQuery
		}
		req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.SetBasicAuth(rep.Creds.ServerID, rep.Creds.Secret)
		if ct := r.Header.Get("Content-Type"); ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		cl := rep.Client
		if cl == nil {
			cl = &http.Client{Timeout: 60 * time.Second}
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
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			w.Header().Set("Retry-After", ra)
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
	}
}
