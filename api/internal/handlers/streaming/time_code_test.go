package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// failingGate is a time keeper that cannot read or count anything.
type failingGate struct{ err error }

func (g failingGate) Admit(context.Context, *principal.Principal, string) (parental.TimeState, error) {
	return parental.TimeState{}, g.err
}
func (g failingGate) Opened(context.Context, string, string, string, string) error { return g.err }
func (g failingGate) Beat(context.Context, string, string) (parental.TimeState, error) {
	return parental.TimeState{}, g.err
}
func (g failingGate) Closed(context.Context, string) error { return g.err }

// Story 57.14: at each place a stream session meets the time keeper, a time that
// cannot be read or counted refuses with 503, Retry-After and the code
// parental_time_unknown, so an app tells it from any other 503.
func TestTimeUnknownAnswersItsOwnCode(t *testing.T) {
	h := &Handler{Time: failingGate{err: errors.New("no database")}}
	p := &principal.Principal{UserID: "u1"}
	calls := map[string]func(w http.ResponseWriter, r *http.Request) bool{
		"admit":    func(w http.ResponseWriter, r *http.Request) bool { return h.timeAdmit(w, r, p, "lib") },
		"opened":   func(w http.ResponseWriter, r *http.Request) bool { return h.timeOpened(w, r, "u1", "lib", "v1", "s1") },
		"progress": func(w http.ResponseWriter, r *http.Request) bool { return h.timeBeat(w, r, "u1", "s1", nil) },
	}
	for name, call := range calls {
		rec := httptest.NewRecorder()
		if call(rec, httptest.NewRequest(http.MethodPost, "/api/stream/sessions", nil)) {
			t.Errorf("%s: let through with a time that is not known", name)
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
			t.Errorf("%s: %d Retry-After %q, want 503 and 5", name, rec.Code, rec.Header().Get("Retry-After"))
		}
		if CodeTimeUnknown != "parental_time_unknown" || body["code"] != CodeTimeUnknown {
			t.Errorf("%s: code = %v, want parental_time_unknown", name, body["code"])
		}
	}
	// the time being over is not this code
	over := &Handler{Time: overGate{}}
	rec := httptest.NewRecorder()
	if over.timeAdmit(rec, httptest.NewRequest(http.MethodPost, "/api/stream/sessions", nil), p, "lib") {
		t.Fatal("let through with the time over")
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusForbidden || body["code"] != "parental_time" {
		t.Errorf("time over: %d %v", rec.Code, body)
	}
}

type overGate struct{ failingGate }

func (overGate) Admit(context.Context, *principal.Principal, string) (parental.TimeState, error) {
	next := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	return parental.TimeState{Reason: parental.ReasonLimitReached, NextAllowedAt: &next}, nil
}
