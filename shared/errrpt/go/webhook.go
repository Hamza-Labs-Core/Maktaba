package errrpt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// WebhookSink is the built-in error-alert sink (Story 21.5 AC-2). It
// posts a small JSON body to a configured URL — the body uses a `text`
// field, which Slack and Discord incoming webhooks both accept and a
// generic receiver can parse, so one sink covers all three without a
// vendor SDK.
//
// Rate limiting (AC-2): a token bucket of MaxPerMin (default 10) tokens,
// refilled once per minute. When the bucket is empty the report is
// dropped (not queued) and a single suppression notice is emitted; this
// is the backoff/suppress behaviour the AC requires and bounds outbound
// volume during an error storm. Story 21.5 EC1 — never block the
// caller, never grow an unbounded queue.
type WebhookSink struct {
	URL    string
	Client *http.Client

	mu          sync.Mutex
	tokens      int
	maxPerMin   int
	windowStart time.Time
	dropped     int
	now         func() time.Time // injectable for tests
}

// NewWebhookSink builds a sink. url == "" returns nil so callers can do
// `errrpt.New(log, NewWebhookSink(os.Getenv("MAKTABA_ERROR_WEBHOOK")))`
// and stay default-off when the env var is unset (Story 21.5 AC-3 /
// 21.8 AC-1 — no outbound by default).
func NewWebhookSink(url string) *WebhookSink {
	if url == "" {
		return nil
	}
	return &WebhookSink{
		URL:       url,
		Client:    &http.Client{Timeout: 5 * time.Second},
		maxPerMin: 10,
		now:       time.Now,
	}
}

// WithMaxPerMin overrides the default 10/min budget.
func (s *WebhookSink) WithMaxPerMin(n int) *WebhookSink {
	if s == nil {
		return nil
	}
	if n < 1 {
		n = 1
	}
	s.maxPerMin = n
	return s
}

// allow returns whether a send is permitted now and, when it is the
// first send after a suppression window, how many were dropped while
// suppressed (so the caller can annotate the alert).
func (s *WebhookSink) allow() (ok bool, droppedSince int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.windowStart.IsZero() || now.Sub(s.windowStart) >= time.Minute {
		s.windowStart = now
		s.tokens = s.maxPerMin
		d := s.dropped
		s.dropped = 0
		if s.tokens > 0 {
			s.tokens--
			return true, d
		}
		return false, d
	}
	if s.tokens > 0 {
		s.tokens--
		return true, 0
	}
	s.dropped++
	return false, 0
}

// Send posts the report. A nil sink, an empty URL, or an exhausted rate
// budget all return nil-or-a-soft-error without ever blocking the
// caller longer than the HTTP client timeout. A typo'd / unreachable
// URL returns an error that Reporter.Capture downgrades to a warn —
// the original error is never masked (Story 21.5 EC2).
func (s *WebhookSink) Send(ctx context.Context, r Report) error {
	if s == nil || s.URL == "" {
		return nil
	}
	ok, droppedSince := s.allow()
	if !ok {
		// Suppressed: drop rather than queue. Not an error — this is
		// the designed backoff (AC-2).
		return nil
	}

	text := fmt.Sprintf("[maktaba] %s error %s: %s",
		r.Category, r.ErrorID, r.Message)
	if droppedSince > 0 {
		text = fmt.Sprintf("%s (%d alert(s) suppressed by rate limit since last send)",
			text, droppedSince)
	}
	body, err := json.Marshal(map[string]any{
		"text":        text,       // Slack/Discord/generic compatible
		"error_id":    r.ErrorID,  // structured fields for generic sinks
		"category":    r.Category, //
		"occurred_at": r.OccurredAt.Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("errrpt: marshal webhook body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL,
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("errrpt: build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("errrpt: webhook post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("errrpt: webhook returned %d", resp.StatusCode)
	}
	return nil
}
