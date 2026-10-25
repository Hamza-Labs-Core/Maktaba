package placement

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Each request a node serves is counted by its class and the status it got (story 35.15:
// maktaba_node_proxy_requests_total); a handler that writes no status counts 200, and the
// writer still flushes (event streams through the proxy).
func TestDispatcherCountsServed(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/api/local", func(http.ResponseWriter, *http.Request) {})
	r.Get("/api/any", func(http.ResponseWriter, *http.Request) {})
	type served struct {
		class  Class
		status int
	}
	var got []served
	d := &Dispatcher{Routes: r,
		Table: append(Exact(Local, []string{http.MethodGet}, "/api/local"), Exact(Any, []string{http.MethodGet}, "/api/any")...),
		Local: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, ok := w.(http.Flusher); !ok {
				t.Error("the counted writer does not flush")
			}
		}),
		Proxy:  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }),
		Paused: func() string { return "paused: update the home" },
		Served: func(c Class, status int) { got = append(got, served{c, status}) },
	}
	for _, p := range []string{"/api/local", "/api/any", "/api/elsewhere"} {
		d.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	want := []served{{Local, http.StatusOK}, {Any, http.StatusServiceUnavailable}, {Home, http.StatusBadGateway}}
	if len(got) != len(want) {
		t.Fatalf("served %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("served %v, want %v", got, want)
		}
	}
}
