package streaming

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// TestCastGrantRefusalsBeforeTheDatabase: who may not mint a cast grant is refused before
// the item is looked up (the Postgres rows are in router/p58_cast_pg_test.go).
func TestCastGrantRefusalsBeforeTheDatabase(t *testing.T) {
	const user = "00000000-0000-4000-8000-0000580700aa"
	h := &CastGrants{}
	for _, c := range []struct {
		name string
		p    *principal.Principal
		body string
		want int
		code string
	}{
		{"no one", nil, `{"video_id":"x"}`, http.StatusUnauthorized, ""},
		{"a cast grant", &principal.Principal{UserID: user, Source: principal.SourceCast}, `{"video_id":"x"}`, http.StatusForbidden, "cast_scope"},
		{"the admin token", &principal.Principal{Source: principal.SourceAdminToken, IsAdmin: true}, `{"video_id":"x"}`, http.StatusForbidden, "cast_needs_person"},
		{"no person id", &principal.Principal{UserID: "admin", Source: principal.SourceJWT}, `{"video_id":"x"}`, http.StatusForbidden, "cast_needs_person"},
		{"through Maktaba Cloud", &principal.Principal{UserID: user, Source: principal.SourceJWT, CloudScope: "remote"}, `{"video_id":"x"}`, http.StatusForbidden, "cast_via_cloud"},
		{"neither item", &principal.Principal{UserID: user, Source: principal.SourceJWT}, `{}`, http.StatusUnprocessableEntity, ""},
		{"both items", &principal.Principal{UserID: user, Source: principal.SourceJWT}, `{"video_id":"a","queue_id":"b"}`, http.StatusUnprocessableEntity, ""},
		{"an unknown field", &principal.Principal{UserID: user, Source: principal.SourceJWT}, `{"video_id":"a","device":"tv"}`, http.StatusBadRequest, ""},
		{"a malformed video id", &principal.Principal{UserID: user, Source: principal.SourceJWT}, `{"video_id":"not-an-id"}`, http.StatusBadRequest, ""},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/cast/grants", strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		if c.p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
		}
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body)
			continue
		}
		if c.code != "" && !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) {
			t.Errorf("%s: body %s has no code %s", c.name, rec.Body, c.code)
		}
	}
}
