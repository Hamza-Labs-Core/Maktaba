// Package mailwebhook handles Resend's Svix-signed delivery-event webhooks
// (POST /v1/mail/webhook/resend): email.bounced, email.complained, email.delivery_delayed, and
// email.failed. While no signing secret is configured, the route answers 404.
package mailwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail/outbox"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// Path is the Resend webhook endpoint on the user listener.
const Path = "/v1/mail/webhook/resend"

// EventRecorder persists a verified webhook event idempotently (satisfied by *outbox.Outbox).
type EventRecorder interface {
	RecordEvent(ctx context.Context, ev mail.WebhookEvent) (outbox.EventOutcome, error)
}

// Deps wires the Resend webhook handler.
type Deps struct {
	Secret func() (string, error)
	Store  EventRecorder
	Abuse  *abuse.Detector
	Logger *slog.Logger
	Now    func() time.Time
}

// Mount attaches POST /v1/mail/webhook/resend to r.
func Mount(r chi.Router, d *Deps) {
	r.Post(Path, d.Resend)
}

// Resend verifies Svix headers and records tracked delivery events.
func (d *Deps) Resend(w http.ResponseWriter, r *http.Request) {
	if d == nil || d.Secret == nil || d.Store == nil {
		http.NotFound(w, r)
		return
	}
	secret, err := d.Secret()
	if errors.Is(err, config.ErrMissing) || secret == "" {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if d.Logger != nil {
			d.Logger.Error("mail webhook: signing secret unreadable", "event", "mail_webhook_secret_error", "err", err)
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "webhook_unavailable"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read_failed"})
		return
	}
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	ev, err := mail.ParseResendWebhook(secret, r.Header, body, now)
	if errors.Is(err, mail.ErrBadWebhookSignature) {
		if d.Abuse != nil {
			if ip := middleware.GetClientIP(r.Context()); ip != "" {
				_ = d.Abuse.Record(r.Context(), abuse.Subject{Kind: "ip", ID: ip}, abuse.KindWebhookForgery, 2,
					map[string]any{"provider": "resend"})
			}
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_signature"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_event"})
		return
	}
	if !mail.TrackedWebhookEvent(ev.Type) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if _, err := d.Store.RecordEvent(r.Context(), ev); err != nil {
		if d.Logger != nil {
			d.Logger.Error("mail webhook: failed to record event", "event", "mail_webhook_error", "event_id", ev.EventID, "type", ev.Type, "err", err)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "record_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
