package mailwebhook

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail/outbox"
)

type fakeRecorder struct {
	events []mail.WebhookEvent
	seen   map[string]bool
	err    error
}

func (f *fakeRecorder) RecordEvent(_ context.Context, ev mail.WebhookEvent) (outbox.EventOutcome, error) {
	if f.err != nil {
		return outbox.EventOutcome{}, f.err
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[ev.EventID] {
		return outbox.EventOutcome{Duplicate: true}, nil
	}
	f.seen[ev.EventID] = true
	f.events = append(f.events, ev)
	return outbox.EventOutcome{Matched: true}, nil
}

func TestResendWebhookRoute(t *testing.T) {
	sec := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	t.Run("404 when secret not configured", func(t *testing.T) {
		r := chi.NewRouter()
		Mount(r, &Deps{
			Secret: func() (string, error) { return "", config.ErrMissing },
			Store:  &fakeRecorder{},
		})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("code = %d, want 404", rec.Code)
		}
	})

	t.Run("signature checks, tracked events, idempotency, privacy", func(t *testing.T) {
		var logs bytes.Buffer
		store := &fakeRecorder{}
		r := chi.NewRouter()
		Mount(r, &Deps{
			Secret: func() (string, error) { return sec, nil },
			Store:  store,
			Logger: slog.New(slog.NewTextHandler(&logs, nil)),
			Now:    func() time.Time { return now },
		})

		send := func(id string, ts time.Time, body string, sigOverride string) *httptest.ResponseRecorder {
			tsStr := strconv.FormatInt(ts.Unix(), 10)
			sig := sigOverride
			if sig == "" {
				var err error
				sig, err = mail.SignSvix(sec, id, tsStr, []byte(body))
				if err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(body))
			req.Header.Set("svix-id", id)
			req.Header.Set("svix-timestamp", tsStr)
			req.Header.Set("svix-signature", sig)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			return rec
		}

		// Invalid signature -> 400.
		if rec := send("msg_bad", now, `{"type":"email.bounced","data":{"email_id":"e1"}}`, "v1,AAAA"); rec.Code != http.StatusBadRequest {
			t.Fatalf("bad sig: %d %s", rec.Code, rec.Body)
		}
		// Stale timestamp -> 400.
		if rec := send("msg_old", now.Add(-10*time.Minute), `{"type":"email.bounced","data":{"email_id":"e1"}}`, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("stale ts: %d %s", rec.Code, rec.Body)
		}
		// Valid hard bounce (with PII in payload that must never be stored or logged).
		payload := `{"type":"email.bounced","created_at":"2026-11-25T11:59:50Z","data":{"email_id":"e1","to":["private@example.test"],"bounce":{"type":"Permanent","message":"private@example.test unknown"}}}`
		if rec := send("msg_1", now, payload, ""); rec.Code != http.StatusOK {
			t.Fatalf("valid bounce: %d %s", rec.Code, rec.Body)
		}
		// Replay of same svix-id -> 200, idempotent.
		if rec := send("msg_1", now, payload, ""); rec.Code != http.StatusOK {
			t.Fatalf("replay: %d %s", rec.Code, rec.Body)
		}
		// Untracked event (email.sent) -> 200, not recorded.
		if rec := send("msg_sent", now, `{"type":"email.sent","data":{"email_id":"e1"}}`, ""); rec.Code != http.StatusOK {
			t.Fatalf("email.sent: %d %s", rec.Code, rec.Body)
		}
		if len(store.events) != 1 || store.events[0].EmailID != "e1" || store.events[0].BounceType != mail.BounceHard {
			t.Fatalf("recorded = %+v", store.events)
		}
		// Store failure -> 500 so Resend retries.
		store.err = errors.New("db down")
		if rec := send("msg_err", now, payload, ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("store err: %d %s", rec.Code, rec.Body)
		}
		if strings.Contains(logs.String(), "private@example.test") {
			t.Fatalf("email leaked to logs: %s", logs.String())
		}
	})
}
