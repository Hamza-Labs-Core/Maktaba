package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The streaming 409: problem+json naming the code and the component.
func TestRequireComponent(t *testing.T) {
	on := true
	h := RequireComponent(func(id string) bool { return on && id == "livetv.hdhomerun" }, "livetv.hdhomerun")(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	call := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/discover.json", nil))
		return rec
	}
	if rec := call(); rec.Code != http.StatusTeapot {
		t.Fatalf("on: %d", rec.Code)
	}
	on = false
	rec := call()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict || rec.Header().Get("Content-Type") != "application/problem+json; charset=utf-8" ||
		body["code"] != "component_off" || body["component"] != "livetv.hdhomerun" || body["status"] != float64(409) {
		t.Fatalf("off: %d %v", rec.Code, body)
	}
	if RequireComponent(nil, "x")(h) == nil {
		t.Fatal("nil gate")
	}
}
