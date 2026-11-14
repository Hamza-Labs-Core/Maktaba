package cloudlink

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// BroadcastAccessPrefix is the loopback prefix of the home's viewer-access calls (story 50.26).
const BroadcastAccessPrefix = "/admin/cloud-link/broadcast/"

// The cloud routes a home may reach through the agent: its channels' access, its viewer
// limits, its invites and an invite's devices (cloud/internal/handlers/broadcast.MountAccessAgent).
// Nothing else under /v1/broadcast is passed on.
var broadcastAccessRoutes = []struct {
	method string
	path   *regexp.Regexp
}{
	{http.MethodGet, regexp.MustCompile(`^access$`)},
	{http.MethodPut, regexp.MustCompile(`^channels/[A-Za-z0-9_-]{1,64}/access$`)},
	{http.MethodPut, regexp.MustCompile(`^viewer-limits$`)},
	{http.MethodGet, regexp.MustCompile(`^invites$`)},
	{http.MethodPost, regexp.MustCompile(`^invites$`)},
	{http.MethodDelete, regexp.MustCompile(`^invites/[A-Za-z0-9_-]{1,64}$`)},
	{http.MethodGet, regexp.MustCompile(`^invites/[A-Za-z0-9_-]{1,64}/devices$`)},
	{http.MethodDelete, regexp.MustCompile(`^invites/[A-Za-z0-9_-]{1,64}/devices/[A-Za-z0-9_-]{1,64}$`)},
}

// MaxBroadcastAccessBody is the largest request the agent passes on (an invite or a setting).
const MaxBroadcastAccessBody = 64 << 10

// BroadcastAccessHandler serves the home's viewer-access calls on loopback and passes each to
// the cloud as the home (the server's secret, which only this agent holds):
//
//	<method> /admin/cloud-link/broadcast/<route>  ->  <method> /v1/broadcast/<route>
//
// for the routes above only. Who on the home may make these calls is the home API's rule
// (api/internal/broadcastaccess: owner or admin); the cloud checks the channels are the home's.
// The cloud's answer comes back as it is; an unreachable cloud is a 502.
func BroadcastAccessHandler(rep *LinkReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := strings.TrimPrefix(r.URL.Path, BroadcastAccessPrefix)
		if sub == r.URL.Path || !broadcastAccessAllowed(r.Method, sub) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		var body io.Reader
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			body = http.MaxBytesReader(w, r.Body, MaxBroadcastAccessBody)
		}
		req, err := http.NewRequestWithContext(ctx, r.Method,
			strings.TrimRight(rep.CloudAPI, "/")+"/v1/broadcast/"+sub, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.SetBasicAuth(rep.Creds.ServerID, rep.Creds.Secret)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		cl := rep.Client
		if cl == nil {
			cl = &http.Client{Timeout: 20 * time.Second}
		}
		resp, err := cl.Do(req)
		if err != nil {
			http.Error(w, "the cloud is out of reach", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
	}
}

func broadcastAccessAllowed(method, sub string) bool {
	for _, rt := range broadcastAccessRoutes {
		if rt.method == method && rt.path.MatchString(sub) {
			return true
		}
	}
	return false
}
