package plugins

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// 54.04 T09: only this machine's own processes mint. No one: 401; a viewer, an admin and
// a cloud-linked play sign-in: 403. (The machine's 200 is in the Postgres test.)
func TestMintIsMachineOnly(t *testing.T) {
	r := chi.NewRouter()
	(&Invocations{Tokens: pl.NewTokens(nil)}).Mount(r)
	play := &principal.Principal{UserID: "u3", IsAdmin: true}
	play.ApplyCloudScope(principal.CloudScopePlay, nil)
	cases := []struct {
		name string
		p    *principal.Principal
		want int
	}{
		{"no one", nil, 401},
		{"viewer", &principal.Principal{UserID: "u1"}, 403},
		{"admin", &principal.Principal{UserID: "u2", IsAdmin: true, AccessAllLibraries: true}, 403},
		{"cloud play", play, 403},
	}
	for _, c := range cases {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			path := "/internal/plugins/invocations"
			if method == http.MethodDelete {
				path += "/inv_x"
			}
			req := httptest.NewRequest(method, path, strings.NewReader(`{"job_id":1,"grants":["tags.write"]}`))
			if c.p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != c.want {
				t.Errorf("%s %s: %d, want %d", c.name, method, w.Code, c.want)
			}
		}
	}
}

// A machine with the wrong local token is refused too.
func TestMintWrongLocalToken(t *testing.T) {
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("right"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", f)
	r := chi.NewRouter()
	(&Invocations{Tokens: pl.NewTokens(nil)}).Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/internal/plugins/invocations", strings.NewReader(`{"job_id":1}`))
	req.Header.Set(middleware.LocalTokenHeader, "wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/internal/plugins/invocations", strings.NewReader(`not json`))
	req.Header.Set(middleware.LocalTokenHeader, "right")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("right token, bad body: %d", w.Code)
	}
}
