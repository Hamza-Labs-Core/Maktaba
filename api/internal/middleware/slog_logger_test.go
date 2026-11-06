package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

func TestSLogLogger_LevelsAndPanics(t *testing.T) {
	var buf bytes.Buffer
	prev := mlog.Default
	mlog.Default = slog.New(slog.NewTextHandler(&buf, nil))
	t.Cleanup(func() { mlog.Default = prev })

	h := SLogLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fail":
			w.WriteHeader(http.StatusBadGateway)
		case "/panic":
			panic("boom")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fail", nil))
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
		t.Error("the panic should go on to the Recoverer")
	}()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %q", lines)
	}
	for i, want := range []string{"level=INFO path=/ok status=200", "level=WARN path=/fail status=502", "level=WARN path=/panic status=500"} {
		lvl, rest, _ := strings.Cut(want, " ")
		if !strings.Contains(lines[i], lvl) || !strings.Contains(lines[i], rest) {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
}

// Epic 53 story 53.01: the request's line names the signed-in user by user_h
// (their hash under the home's log key), never by user_id.
func TestSLogLogger_NamesTheUserByHash(t *testing.T) {
	var buf bytes.Buffer
	prev := mlog.Default
	mlog.Default = mlog.NewLogger(mlog.Options{Service: "api", Env: "prod", Output: &buf})
	key := bytes.Repeat([]byte{7}, mlog.LogKeySize)
	mlog.SetUserHashKey(key)
	t.Cleanup(func() { mlog.Default = prev; mlog.SetUserHashKey(nil) })

	h := SLogLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the sign-in middleware, below the logger
		ctx := principal.WithPrincipal(r.Context(), &principal.Principal{UserID: "42"})
		mlog.From(ctx).Info("handler_line")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/me", nil))

	want := `"user_h":"` + mlog.UserHash(key, "42") + `"`
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", lines)
	}
	for _, l := range lines {
		if !strings.Contains(l, want) || strings.Contains(l, "user_id") {
			t.Errorf("line %s: want %s and no user_id", l, want)
		}
	}
}
