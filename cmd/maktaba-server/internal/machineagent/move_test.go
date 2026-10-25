package machineagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A working session straight to the home moves to the link only when the home requires
// it (story 35.16): a move drops the session for a moment, which an update under way
// sees as the machine going offline.
func TestMovesToTheLinkOnlyWhenRequired(t *testing.T) {
	mode := "prefer"
	up := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-Maktaba-Link-Mode", mode)
		http.Error(w, "a WebSocket upgrade is expected", http.StatusBadRequest)
	}))
	defer srv.Close()
	a := &Agent{ID: Identity{LocalURL: "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/cluster/link/local/agent"}}
	if a.linkRequired(context.Background()) {
		t.Fatal("moved while the home prefers the link")
	}
	mode = "require"
	if !a.linkRequired(context.Background()) {
		t.Fatal("stayed on a plain session the home requires the link for")
	}
	up = false
	if a.linkRequired(context.Background()) {
		t.Fatal("moved to a link that is not up")
	}
}
