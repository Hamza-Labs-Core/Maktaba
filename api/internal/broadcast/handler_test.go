package broadcast

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// T09 (and the acceptance's PUT): a user who is not an admin gets 403 on every Broadcast
// route, no one at all 401, before anything is read or sent to the cloud.
func TestBroadcastRoutesAreAdminOnly(t *testing.T) {
	cloud := &fakeCloud{}
	r := chi.NewRouter()
	(&Routes{Service: &Service{Store: &Store{}, Cloud: cloud}}).Mount(r)
	param := regexp.MustCompile(`\{[^}]+\}`)
	for _, route := range Paths {
		method, pattern, _ := strings.Cut(route, " ")
		path := param.ReplaceAllString(pattern, "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60")
		for _, c := range []struct {
			who  string
			p    *principal.Principal
			want int
		}{
			{"no one", nil, http.StatusUnauthorized},
			{"viewer", &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}, http.StatusForbidden},
		} {
			req := httptest.NewRequest(method, path, strings.NewReader(`{"confirm_rights":true}`))
			if c.p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("%s as %s: %d, want %d", route, c.who, rec.Code, c.want)
			}
		}
	}
	if cloud.calls() != 0 {
		t.Fatalf("a refused caller reached the cloud: %d calls", cloud.calls())
	}
}
