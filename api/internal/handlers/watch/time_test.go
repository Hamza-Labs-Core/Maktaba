package watch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

type fixedTime struct {
	st  parental.TimeState
	err error
}

func (f fixedTime) Time(context.Context, *principal.Principal, string) (parental.TimeState, error) {
	return f.st, f.err
}

// Story 57.14: a watch is refused with 403 parental-time while the account's time
// is over, and with 503 and Retry-After when the time cannot be read (never let
// through on an error).
func TestTimeOKAnswers(t *testing.T) {
	next := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		reader TimeReader
		ok     bool
		status int
	}{
		{"no keeper", nil, true, 200},
		{"allowed", fixedTime{st: parental.TimeState{Allowed: true}}, true, 200},
		{"over", fixedTime{st: parental.TimeState{Reason: parental.ReasonLimitReached, NextAllowedAt: &next}}, false, 403},
		{"not known", fixedTime{err: errors.New("no database")}, false, 503},
	}
	for _, c := range cases {
		h := &Handler{}
		if c.reader != nil {
			h.Time = c.reader
		}
		rec := httptest.NewRecorder()
		ok := h.timeOK(rec, httptest.NewRequest("POST", "/api/watch/start", nil), user(), "lib")
		if ok != c.ok || rec.Code != c.status {
			t.Errorf("%s: ok %v status %d, want %v %d", c.name, ok, rec.Code, c.ok, c.status)
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		switch c.status {
		case 403:
			if body["type"] != ProblemParentalTime || body["code"] != "parental_time" ||
				body["reason"] != parental.ReasonLimitReached || body["next_allowed_at"] != "2026-11-12T01:00:00Z" {
				t.Errorf("%s: body = %v", c.name, body)
			}
		case 503:
			if rec.Header().Get("Retry-After") != "5" {
				t.Errorf("%s: Retry-After = %q, want 5", c.name, rec.Header().Get("Retry-After"))
			}
		}
	}
}
