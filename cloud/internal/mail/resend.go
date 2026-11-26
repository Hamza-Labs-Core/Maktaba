package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ResendURL is Resend's API.
const ResendURL = "https://api.resend.com"

// SendError is a mail the provider did not take. It names no address, subject, link or key:
// only the answer's status and the provider's own short error name.
type SendError struct {
	Status int    // 0: no answer (the network, a timeout)
	Name   string // the provider's error name ("rate_limit_exceeded")
	// Temporary: the same mail may be taken later. Refused: the key or the sending domain, not
	// the mail, is what was refused (401, 403): later too, once a person sets it right.
	Temporary, Refused, Quota bool
	RetryAfter                time.Duration // the provider's, 0 without one
	err                       error
}

func (e *SendError) Error() string {
	switch {
	case e.Status == 0 && e.err != nil:
		return "resend: no answer: " + e.err.Error()
	case e.Name != "":
		return fmt.Sprintf("resend: %d %s", e.Status, e.Name)
	}
	return fmt.Sprintf("resend: %d", e.Status)
}

// Unwrap is what the network said.
func (e *SendError) Unwrap() error { return e.err }

// Resend sends mails through resend.com's HTTPS API. One Send makes at most Tries attempts,
// each bounded by Timeout: a network failure, a 5xx and a 429 are tried again with the same
// idempotency key (the message's ID), so a mail whose answer was lost is delivered once; any
// other 4xx is not. At most InFlight sends are under way; the rest wait for their context.
type Resend struct {
	// Key reads the API key at each send, so a new key in the secret file needs no restart.
	Key     func() (string, error)
	From    string
	ReplyTo string

	URL      string        // default ResendURL
	Client   *http.Client  // default: no redirects followed
	Timeout  time.Duration // one attempt, default 10 s
	Tries    int           // default 3
	MaxWait  time.Duration // the longest wait between two tries, default 10 s; a longer Retry-After goes back to the caller
	BaseWait time.Duration // the first wait without a Retry-After, default 500 ms
	slots    chan struct{}
}

// NewResend returns a driver with inFlight sends at once (default 4).
func NewResend(key func() (string, error), from, replyTo string, inFlight int) *Resend {
	if inFlight <= 0 {
		inFlight = 4
	}
	return &Resend{Key: key, From: from, ReplyTo: replyTo, URL: ResendURL,
		Client: &http.Client{
			// the key goes to one host: a redirect is an error, never followed
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		Timeout: 10 * time.Second, Tries: 3, MaxWait: 10 * time.Second, BaseWait: 500 * time.Millisecond,
		slots: make(chan struct{}, inFlight)}
}

type resendMail struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	Text    string            `json:"text"`
	HTML    string            `json:"html,omitempty"`
	ReplyTo string            `json:"reply_to,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Tags    []resendTag       `json:"tags,omitempty"`
}

type resendTag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// oneLine keeps a header's value on one line.
func oneLine(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

// Send delivers m, or says why not (*SendError, or the context's error).
func (r *Resend) Send(ctx context.Context, m Message) error {
	_, err := r.SendID(ctx, m)
	return err
}

// SendID delivers m and returns Resend's message id, or says why not.
func (r *Resend) SendID(ctx context.Context, m Message) (string, error) {
	if m.ID == "" {
		return "", errors.New("resend: the message has no ID (its idempotency key)")
	}
	key, err := r.Key()
	if err != nil {
		// the file's path at most, never what is in it
		return "", &SendError{Temporary: true, Refused: true, Name: "no_api_key", err: err}
	}
	body, err := json.Marshal(resendMail{From: r.From, To: []string{m.To}, Subject: oneLine(m.Subject), Text: m.Body,
		HTML: m.HTML, ReplyTo: r.ReplyTo,
		Headers: map[string]string{"Auto-Submitted": "auto-generated"},
		Tags:    []resendTag{{Name: "kind", Value: tagValue(m.Kind)}}})
	if err != nil {
		return "", err
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	for try := 1; ; try++ {
		id, err := r.once(ctx, key, "maktaba-"+m.ID, body)
		if err == nil {
			return id, nil
		}
		// a cancelled caller is not the provider's failure
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var se *SendError
		if !errors.As(err, &se) || !se.Temporary || se.Refused || se.Quota || try >= r.Tries {
			return "", err
		}
		wait := se.RetryAfter
		if wait == 0 {
			wait = r.BaseWait << (try - 1)
			wait += time.Duration(rand.Int64N(int64(wait)/4 + 1))
		}
		if wait > r.MaxWait {
			return "", err
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return "", ctx.Err()
		}
	}
}

// once is one attempt.
func (r *Resend) once(ctx context.Context, key, idem string, body []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.URL, "/")+"/emails", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idem)
	req.Header.Set("User-Agent", "maktaba-cloud")
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", &SendError{Temporary: true, err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode/100 == 2 {
		var ok struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &ok)
		return strings.TrimSpace(ok.ID), nil
	}
	// the answer's message may quote the address: only its short name is kept
	var a struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &a)
	se := &SendError{Status: resp.StatusCode, Name: errorName(a.Name), RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		se.Temporary = true
		se.Quota = strings.HasSuffix(se.Name, "_quota_exceeded")
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		se.Temporary, se.Refused = true, true
	case resp.StatusCode == http.StatusBadRequest && strings.HasPrefix(a.Message, "API key is invalid"):
		// a malformed key: every mail is refused so, and none is at fault
		se.Temporary, se.Refused = true, true
		se.Name = "invalid_api_key"
	case resp.StatusCode == http.StatusConflict && se.Name == "concurrent_idempotent_requests":
		se.Temporary = true // the first attempt is still being taken
	case resp.StatusCode >= 500:
		se.Temporary = true
	}
	return "", se
}

// retryAfter reads a Retry-After: seconds, or a date.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(min(n, 24*3600)) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil && at.After(now) {
		return min(at.Sub(now), 24*time.Hour)
	}
	return 0
}

// errorName keeps a provider's error name only when it is one: a short lower-case word.
func errorName(s string) string {
	if len(s) > 64 {
		return ""
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return ""
		}
	}
	return s
}

// tagValue keeps what a Resend tag may hold.
func tagValue(s string) string {
	return strings.Map(func(c rune) rune {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			return c
		}
		return '_'
	}, s)
}
