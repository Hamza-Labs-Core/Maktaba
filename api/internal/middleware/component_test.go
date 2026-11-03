package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// T10: with the gate off the route answers 409 problem+json naming the component;
// with it on (or no runner) the route runs.
func TestRequireComponent(t *testing.T) {
	r := runner.New(runner.Options{Host: "api"})
	defer r.Run(cancelled(), runner.Source{})
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := RequireComponent(r, "livetv")(ok)
	call := func(h http.Handler) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/tv/changes", nil))
		return rec
	}
	if rec := call(h); rec.Code != http.StatusTeapot {
		t.Fatalf("livetv on: %d", rec.Code)
	}
	r.Apply(map[string]store.Desired{"livetv": {ID: "livetv", Want: components.WantInstalled, Generation: 1}})
	rec := call(h)
	if rec.Code != http.StatusConflict || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("livetv off: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "component_off" || body["component"] != "livetv" || body["status"] != float64(409) {
		t.Fatalf("problem %v", body)
	}
	if rec := call(RequireComponent(nil, "livetv")(ok)); rec.Code != http.StatusTeapot {
		t.Fatalf("no runner: %d", rec.Code)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown component was accepted")
		}
	}()
	RequireComponent(r, "nope")
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
