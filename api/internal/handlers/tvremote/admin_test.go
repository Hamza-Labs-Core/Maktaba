package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite")

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	remote "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

type ext struct{ s map[string]any }

func (e *ext) ExtensionSettings(context.Context, string) (map[string]any, bool, error) {
	return e.s, true, nil
}
func (e *ext) SaveExtensionSetting(_ context.Context, _, k, v string) error { e.s[k] = v; return nil }
func (e *ext) ClearExtension(context.Context, string) error                 { e.s = map[string]any{}; return nil }

func server(t *testing.T) (http.Handler, *cftest.Server) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`); err != nil {
		t.Fatal(err)
	}
	f := cftest.New(t)
	store := secrets.NewMemory()
	_ = store.Put(context.Background(), remote.SecretAPIToken, []byte("tok-secret"), "")
	in := &remote.Integration{DB: db, Secrets: store, Ext: &ext{s: map[string]any{"account_id": "acc1", "customer_code": "abc123xyz"}},
		NewClient: f.Client,
		CanPush: func(context.Context) (remote.PushAbility, error) {
			return remote.PushAbility{RTMPS: true, Source: "test"}, nil
		}}
	r := chi.NewRouter()
	(&Handler{Integration: in}).Mount(r)
	return r, f
}

func call(h http.Handler, method, path, body string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u", IsAdmin: admin}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRemoteAdminRoutes(t *testing.T) {
	h, _ := server(t)
	for _, rt := range [][2]string{{"GET", "/api/tv/remote"}, {"POST", "/api/tv/remote/test"},
		{"PUT", "/api/tv/remote/enabled"}, {"DELETE", "/api/tv/remote/integration"}} {
		if rec := call(h, rt[0], rt[1], `{}`, false); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a viewer: %d", rt[0], rt[1], rec.Code)
		}
	}
	// AC4 / TC4 over HTTP
	if rec := call(h, "PUT", "/api/tv/remote/enabled", `{"enabled":true}`, true); rec.Code != http.StatusConflict {
		t.Fatalf("enable before a test: %d %s", rec.Code, rec.Body.String())
	}
	rec := call(h, "POST", "/api/tv/remote/test", "", true)
	var res remote.TestResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || !res.OK || len(res.Lines) != 7 {
		t.Fatalf("test: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "tok-secret") || strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatalf("a secret in the answer: %s", rec.Body.String())
	}
	if rec := call(h, "PUT", "/api/tv/remote/enabled", `{"enabled":true}`, true); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"enabled":true`) || !strings.Contains(rec.Body.String(), `"last_test"`) {
		t.Fatalf("enable after a green test: %d %s", rec.Code, rec.Body.String())
	}
	// AC5 needs confirmation
	if rec := call(h, "DELETE", "/api/tv/remote/integration", `{}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("remove without confirm: %d", rec.Code)
	}
	if rec := call(h, "DELETE", "/api/tv/remote/integration", `{"confirm":true}`, true); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), "deleted_signing_key") {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, "GET", "/api/tv/remote", "", true); !strings.Contains(rec.Body.String(), `"enabled":false`) ||
		!strings.Contains(rec.Body.String(), `"token_set":false`) {
		t.Fatalf("status after remove: %s", rec.Body.String())
	}
}
