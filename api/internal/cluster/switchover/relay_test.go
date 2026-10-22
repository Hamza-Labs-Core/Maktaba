package switchover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The cloud relay moved with the home once the agent's tunnel is up on it; it is being
// moved while the agent starts or reconnects, failed after many attempts, and not used
// where this home runs no agent.
func TestTheCloudRelayMovesWithTheHome(t *testing.T) {
	ctx := context.Background()
	if got := relayLink(ctx, ""); got.State != NotUsed {
		t.Fatalf("no agent: %+v", got)
	}
	state := map[string]any{"status": "online", "slug": "my-library"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if state["status"] != "online" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(state)
	}))
	defer srv.Close()
	if got := relayLink(ctx, srv.URL); got.State != Moved || got.Detail != "my-library" {
		t.Fatalf("online: %+v", got)
	}
	state = map[string]any{"status": "reconnecting", "attempts": 2, "last_error": "dial: refused"}
	if got := relayLink(ctx, srv.URL); got.State != Pending || got.Detail != "dial: refused" {
		t.Fatalf("reconnecting: %+v", got)
	}
	state = map[string]any{"status": "reconnecting", "attempts": relayLinkTries, "last_error": "auth: bad secret"}
	if got := relayLink(ctx, srv.URL); got.State != Failed || got.Detail != "auth: bad secret" {
		t.Fatalf("failing: %+v", got)
	}
	srv.Close()
	if got := relayLink(ctx, srv.URL); got.State != Pending {
		t.Fatalf("the agent not answering yet: %+v", got)
	}
}
