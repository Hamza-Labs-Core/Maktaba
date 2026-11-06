package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"

	_ "modernc.org/sqlite"
)

// Epic 53 story 53.04 T01b: the recovery pass, as nobody (401), a viewer (403, no
// cookie) and an admin (200, the cookie scoped to /recovery, a 7-day maktaba-recovery JWT).
func TestRecoveryPassRoles(t *testing.T) {
	h := &Handler{Keys: testKeys(t), SecureCookies: true}
	call := func(p *principal.Principal) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/recovery-pass", nil)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		h.RecoveryPass(rec, req)
		return rec
	}
	if rec := call(nil); rec.Code != http.StatusUnauthorized || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("nobody: %d %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
	if rec := call(&principal.Principal{UserID: "v", IsAdmin: false}); rec.Code != http.StatusForbidden ||
		rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("viewer: %d %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
	rec := call(&principal.Principal{UserID: "a1", IsAdmin: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body.String())
	}
	var c *http.Cookie
	for _, k := range rec.Result().Cookies() {
		if k.Name == CookieRecovery {
			c = k
		}
	}
	if c == nil || c.Path != "/recovery" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", c)
	}
	claims, err := jwt.Verify(h.Keys, c.Value, RecoveryAudience)
	if err != nil || claims.Sub != "a1" || !claims.IsAdmin {
		t.Fatalf("pass: %+v %v", claims, err)
	}
	if d := time.Unix(claims.Exp, 0).Sub(time.Unix(claims.Iat, 0)); d != RecoveryPassTTL {
		t.Fatalf("lasts %v, want 7 days", d)
	}
}

// Demotion, deletion and sign-out everywhere write the user to the revocation file, one
// entry per user with the latest time.
func TestRevokeRecoveryPassesWritesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "recovery-revoked.json")
	t.Setenv(EnvRecoveryRevoked, path)
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	for _, e := range []RevokedEntry{{"a1", t0}, {"a2", t0}, {"a1", t0.Add(time.Hour)}} {
		if err := RevokeRecoveryPasses(e.UserID, e.At); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []RevokedEntry
	_ = json.Unmarshal(b, &got)
	if len(got) != 2 || got[1].UserID != "a1" || !got[1].At.Equal(t0.Add(time.Hour)) {
		t.Fatalf("file: %s", b)
	}
	t.Setenv(EnvRecoveryRevoked, "")
	if err := RevokeRecoveryPasses("a3", t0); err != nil {
		t.Fatalf("no file configured is no error: %v", err)
	}
}

// T05: what the console did while the API was down is in audit_log once it starts, by
// whom; the file is emptied; a bad line is skipped.
func TestImportRecoveryActions(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE audit_log (category TEXT, action TEXT, actor_user_id TEXT, target_id TEXT, payload TEXT)`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "recovery-actions.jsonl")
	_ = os.WriteFile(path, []byte(`{"at":"2026-11-06T10:00:00Z","action":"restart","detail":"core.postgres","user_id":"a1"}
not json
{"at":"2026-11-06T10:01:00Z","action":"safe_mode","tray":true}
`), 0o600)
	n, err := ImportRecoveryActions(context.Background(), securityaudit.NewWriter(db), path)
	if err != nil || n != 2 {
		t.Fatalf("imported %d: %v", n, err)
	}
	rows, err := db.Query(`SELECT action, COALESCE(actor_user_id, ''), payload FROM audit_log ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a, u, p string
		_ = rows.Scan(&a, &u, &p)
		got = append(got, a+"|"+u+"|"+p)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "recovery.action|a1|") || !strings.Contains(got[0], `"detail":"core.postgres"`) ||
		!strings.Contains(got[1], `"by":"tray"`) {
		t.Fatalf("rows: %v", got)
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Fatalf("the file should be emptied: %q", b)
	}
}
