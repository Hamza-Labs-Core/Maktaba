package channels

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Post-review: channels.id is a Postgres UUID, so an id that is not one names
// no channel: 404 (DELETE: 204, reorder: 400), never the 500 of a failed
// ::uuid cast.
func badIDCases() []struct {
	method, path, body string
	want               int
} {
	return []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/channels/not-a-uuid", "", http.StatusNotFound},
		{"PATCH", "/api/channels/not-a-uuid", `{"name":"x"}`, http.StatusNotFound},
		{"DELETE", "/api/channels/not-a-uuid", "", http.StatusNoContent},
		{"POST", "/api/channels/not-a-uuid/follow-plan", "", http.StatusNotFound},
		{"PATCH", "/api/channels/not-a-uuid/strip", `{}`, http.StatusNotFound},
		{"DELETE", "/api/channels/not-a-uuid/strip", "", http.StatusNotFound},
		{"GET", "/api/channels/not-a-uuid/logo", "", http.StatusNotFound},
		{"GET", "/api/channels/not-a-uuid/art/watermark", "", http.StatusNotFound},
		{"DELETE", "/api/channels/not-a-uuid/art/logo", "", http.StatusNotFound},
		{"GET", "/api/channels/not-a-uuid/schedule", "", http.StatusNotFound},
		{"POST", "/api/channels/not-a-uuid/regenerate", "", http.StatusNotFound},
		{"POST", "/api/channels/reorder", `[{"id":"not-a-uuid","number":3}]`, http.StatusBadRequest},
	}
}

func runBadIDs(t *testing.T, db *sql.DB) {
	t.Helper()
	r := chi.NewRouter()
	h := &Handler{DB: db, DataDir: t.TempDir()}
	h.Mount(r)
	h.MountSchedule(r)
	e := artEnv{db: db, srv: r}
	for _, tc := range badIDCases() {
		var req *http.Request
		if tc.body != "" {
			req = jsonReq(tc.method, tc.path, tc.body)
		} else {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		}
		if rec, _ := e.do(t, artAdmin, req); rec.Code != tc.want {
			t.Errorf("%s %s: %d, want %d (%s)", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestNonUUIDChannelIDs(t *testing.T) { runBadIDs(t, newBuilderEnv(t).db) }

// The same on real Postgres (skips without MAKTABA_TEST_PG_URL), where the
// cast used to fail.
func TestNonUUIDChannelIDsPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	runBadIDs(t, db)
}
