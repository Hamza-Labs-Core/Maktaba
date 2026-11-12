package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// TestUnregisteredProblemPositiveControl is the registry check's positive control (story
// 60.06 T12), run only by tools/ci/unregistered-problems-control.sh: a handler behind the
// recoverer sends an unregistered type, and this test, which checks only the status, passes.
// The script then expects the CI check to fail naming the slug. The registered type sent
// beside it must not be recorded.
func TestUnregisteredProblemPositiveControl(t *testing.T) {
	if os.Getenv("MAKTABA_PROBLEMS_POSITIVE_CONTROL") == "" {
		t.Skip("run by tools/ci/unregistered-problems-control.sh")
	}
	h := Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/registered" {
			httperror.Write(w, r, httperror.Conflict("", "a registered type"))
			return
		}
		httperror.Write(w, r, &httperror.Error{Type: httperror.Type("positive-control-unregistered-fixture"),
			Title: "conflict", Status: http.StatusConflict})
	}))
	for _, path := range []string{"/unregistered", "/registered"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: status %d", path, w.Code)
		}
	}
}
