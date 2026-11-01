package channels

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// TestGetFollowsLibraryAccess is Epic 48 story 48.3's API half: the web port's
// stream gate asks GET /api/channels/{id} whether a viewer may watch a channel,
// so its answer must follow library access as the guide does (canRead): an
// admin and an all-library viewer read every channel; a viewer limited to
// library A reads library A's and the multi-library channel, and gets 404 (as
// for an unknown id) for library B's; no principal is refused.
func TestGetFollowsLibraryAccess(t *testing.T) {
	db, srv := newLogoServer(t)
	const (
		chA     = "00000000-0000-4000-8000-0000000000a1"
		chB     = "00000000-0000-4000-8000-0000000000b1"
		chMulti = "00000000-0000-4000-8000-0000000000c1"
		unknown = "00000000-0000-4000-8000-0000000000d1"
	)
	for _, id := range []string{chA, chB, chMulti} {
		insertChannel(t, db, id, nil)
	}
	if _, err := db.Exec(`UPDATE channels SET library_id = 'lib-a' WHERE id = $1`, chA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE channels SET library_id = 'lib-b' WHERE id = $1`, chB); err != nil {
		t.Fatal(err)
	}
	admin := &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}
	allLibs := &principal.Principal{UserID: "u", AccessAllLibraries: true}
	libA := &principal.Principal{UserID: "v", Libraries: []string{"lib-a"}}
	cases := []struct {
		name string
		p    *principal.Principal
		id   string
		want int
	}{
		{"no principal", nil, chA, http.StatusForbidden},
		{"admin, library A", admin, chA, 200},
		{"admin, library B", admin, chB, 200},
		{"admin, multi-library", admin, chMulti, 200},
		{"all libraries, library B", allLibs, chB, 200},
		{"all libraries, multi-library", allLibs, chMulti, 200},
		{"library A viewer, library A", libA, chA, 200},
		{"library A viewer, multi-library", libA, chMulti, 200},
		{"library A viewer, library B", libA, chB, http.StatusNotFound},
		{"library A viewer, unknown", libA, unknown, http.StatusNotFound},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/api/channels/"+c.id, nil)
		if c.p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
}
