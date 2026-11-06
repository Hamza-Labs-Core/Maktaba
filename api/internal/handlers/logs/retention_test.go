package logs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
)

// Epic 53 story 53.11 T05/T08: the Retention and detailed-logs routes, as nobody (401), a
// viewer (403) and an admin (the supervisor's answer, with its token; its 400 passed on);
// with no supervisor, 409.
func TestRetentionRoutesRoles(t *testing.T) {
	var seen []string
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(b))
		if strings.Contains(string(b), "91") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"errors":[{"field":"logs.logs_days"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"logs_days":7,"errors_days":30,"reports_days":90,"total_bytes":1073741824}`)
	}))
	defer sup.Close()
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("boot\n"), 0o600)
	h := &Handler{Supervisor: &serversettings.Supervisor{URL: sup.URL, TokenFile: tok}}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(method, path, body string, p *principal.Principal) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	routes := [][3]string{{"GET", "/api/admin/logs/retention", ""}, {"PUT", "/api/admin/logs/retention", `{"logs_days":14}`},
		{"POST", "/api/admin/logs/level", `{"service":"pipeline","level":"debug","minutes":60}`}}
	for _, rt := range routes {
		if c, _ := call(rt[0], rt[1], rt[2], nil); c != http.StatusUnauthorized {
			t.Errorf("%v as nobody: %d", rt, c)
		}
		if c, _ := call(rt[0], rt[1], rt[2], &principal.Principal{UserID: "v"}); c != http.StatusForbidden {
			t.Errorf("%v as a viewer: %d", rt, c)
		}
	}
	if len(seen) != 0 {
		t.Fatalf("refused calls reached the supervisor: %v", seen)
	}
	admin := &principal.Principal{UserID: "a", IsAdmin: true}
	for _, rt := range routes {
		if c, body := call(rt[0], rt[1], rt[2], admin); c != http.StatusOK || !strings.Contains(body, `"logs_days":7`) {
			t.Errorf("%v as an admin: %d %s", rt, c, body)
		}
	}
	want := []string{"GET /config/logs Bearer boot ", `PUT /config/logs Bearer boot {"logs_days":14}`,
		`POST /config/logs/level Bearer boot {"service":"pipeline","level":"debug","minutes":60}`}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("the supervisor saw %q", seen)
	}
	if c, _ := call("PUT", "/api/admin/logs/retention", `{"logs_days":91}`, admin); c != http.StatusBadRequest {
		t.Errorf("out of range: %d", c)
	}
	h.Supervisor = nil
	t.Setenv("MAKTABA_SUPERVISOR_URL", "")
	if c, _ := call("GET", "/api/admin/logs/retention", "", admin); c != http.StatusConflict {
		t.Errorf("no supervisor: %d", c)
	}
}
