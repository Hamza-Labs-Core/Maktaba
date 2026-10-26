package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
