package cloudlink

import (
	"context"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// BroadcastPrefix is the agent's loopback path for home broadcasting (Epic 50 story 50.22).
const BroadcastPrefix = "/admin/cloud-link/broadcast/"

// broadcastRoutes are the only cloud calls the home's API may make through the agent for
// broadcasting, each with the server's own credential: the publisher's state (plan,
// agreement), publishing and unpublishing a channel, a channel's ingest token (story 50.21)
// the revocation list (story 50.25), and the takedown instructions, their acks, the home's
// moderation records and a counter-notice (story 50.24). Anything else is 404: the loopback port
// is no general proxy.
var broadcastRoutes = []struct {
	method string
	path   *regexp.Regexp
}{
	{http.MethodGet, regexp.MustCompile(`^state$`)},
	{http.MethodPost, regexp.MustCompile(`^channels$`)},
	{http.MethodDelete, regexp.MustCompile(`^channels/[0-9a-fA-F-]{36}$`)},
	{http.MethodPost, regexp.MustCompile(`^ingest-token$`)},
	// the signed broadcast revocation list (story 50.25), only this home's entries
	{http.MethodGet, regexp.MustCompile(`^revocations$`)},
	// takedowns and reports (story 50.24): only this home's
	{http.MethodGet, regexp.MustCompile(`^takedowns$`)},
	{http.MethodPost, regexp.MustCompile(`^takedowns/[0-9a-fA-F-]{36}/ack$`)},
	{http.MethodGet, regexp.MustCompile(`^moderation$`)},
	{http.MethodPost, regexp.MustCompile(`^moderation/counter-notices$`)},
}

// BroadcastHandler serves BroadcastPrefix on loopback: the home's API asks the cloud's
// /v1/broadcast/<path> and the agent adds the server id and secret only it holds, passing
// the cloud's status and body back as they came.
func BroadcastHandler(rep *LinkReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !fromLocalProgram(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, BroadcastPrefix)
		allowed := false
		for _, rt := range broadcastRoutes {
			if rt.method == r.Method && rt.path.MatchString(rest) {
				allowed = true
				break
			}
		}
		if !allowed || rest == r.URL.Path {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		var body io.Reader
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			body = io.LimitReader(r.Body, 16<<10)
		}
		req, err := http.NewRequestWithContext(ctx, r.Method, rep.CloudAPI+"/v1/broadcast/"+rest, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
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
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 64<<10))
	}
}

// fromLocalProgram refuses what a web page in a browser on the home machine could send to
// the loopback port (the agent adds the server's secret): a Host that is not loopback (DNS
// rebinding), any Origin or a cross-site Sec-Fetch-Site (a browser's request), and a POST
// that is not application/json (a form or text/plain needs no CORS preflight; JSON does,
// and the agent never answers one).
func fromLocalProgram(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	if r.Header.Get("Origin") != "" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" {
		return false
	}
	if r.Method == http.MethodPost {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			return false
		}
	}
	return true
}
