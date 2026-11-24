package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Providers says whether broadcasting is on (broadcast.enabled), so the web app shows its
// broadcasting pages only then; off is the default (2026-11-24).
func TestProvidersBroadcast(t *testing.T) {
	for _, on := range []bool{false, true} {
		d := &Deps{BroadcastEnabled: on}
		w := httptest.NewRecorder()
		d.Providers(w, httptest.NewRequest(http.MethodGet, "/v1/auth/providers", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out["broadcast"] != on {
			t.Fatalf("broadcast %v: %v", on, out)
		}
		if out["google"] != false || out["apple"] != false {
			t.Fatalf("no OAuth configured: %v", out)
		}
	}
}
