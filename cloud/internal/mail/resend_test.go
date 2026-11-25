package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "re_test_key_123"

// fakeResend answers each request with the next of answers (the last one again after).
type fakeResend struct {
	mu      sync.Mutex
	reqs    []*http.Request
	bodies  []string
	answers []func(w http.ResponseWriter, r *http.Request)
	srv     *httptest.Server
}

func newFake(t *testing.T, answers ...func(w http.ResponseWriter, r *http.Request)) *fakeResend {
	t.Helper()
	f := &fakeResend{answers: answers}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		n := len(f.reqs)
		f.reqs, f.bodies = append(f.reqs, r), append(f.bodies, string(b))
		f.mu.Unlock()
		f.answers[min(n, len(f.answers)-1)](w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeResend) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func answer(status int, body string, header ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

var ok200 = answer(200, `{"id":"49a3999c-0ce1-4ea6-ab68-afcd6dc2e794"}`)

func (f *fakeResend) driver() *Resend {
	r := NewResend(func() (string, error) { return testKey, nil }, "Maktaba <no-reply@maktaba.tv>", "support@maktaba.tv", 2)
	r.URL, r.BaseWait, r.Timeout, r.MaxWait = f.srv.URL, time.Millisecond, 2*time.Second, 1500*time.Millisecond
	return r
}

var testMail = Message{ID: "7c1e", To: "someone@example.test", Subject: "Hello\r\nBcc: x@example.test", Body: "text https://app.example.test/verify-email?token=SECRETTOKEN",
	HTML: "<p>html</p>", Kind: "verify_email", Lang: "en"}

func TestResendSendsOneMail(t *testing.T) {
	f := newFake(t, ok200)
	if err := f.driver().Send(context.Background(), testMail); err != nil {
		t.Fatal(err)
	}
	r := f.reqs[0]
	if r.Method != "POST" || r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer "+testKey ||
		r.Header.Get("Idempotency-Key") != "maktaba-7c1e" || r.Header.Get("User-Agent") == "" || r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("%s %s %v", r.Method, r.URL, r.Header)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(f.bodies[0]), &m); err != nil {
		t.Fatal(err)
	}
	if m["from"] != "Maktaba <no-reply@maktaba.tv>" || m["reply_to"] != "support@maktaba.tv" || m["html"] != "<p>html</p>" ||
		m["text"] != testMail.Body || len(m["to"].([]any)) != 1 || m["to"].([]any)[0] != testMail.To {
		t.Fatalf("%v", m)
	}
	if s := m["subject"].(string); strings.ContainsAny(s, "\r\n") {
		t.Fatalf("a subject over several lines: %q", s)
	}
}

// A 429, a 5xx and a lost answer are tried again with the same idempotency key and the same body.
func TestResendTriesAgain(t *testing.T) {
	cut := func(w http.ResponseWriter, _ *http.Request) {
		c, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = c.Close()
		}
	}
	for name, first := range map[string]func(http.ResponseWriter, *http.Request){
		"429":                answer(429, `{"name":"rate_limit_exceeded"}`),
		"500":                answer(500, `{"name":"application_error"}`),
		"503 of a proxy":     answer(503, `<html>`),
		"the network":        cut,
		"409 being sent yet": answer(409, `{"name":"concurrent_idempotent_requests"}`),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, first, first, ok200)
			if err := f.driver().Send(context.Background(), testMail); err != nil {
				t.Fatal(err)
			}
			if f.calls() != 3 {
				t.Fatalf("%d calls", f.calls())
			}
			for i := range f.reqs {
				if f.reqs[i].Header.Get("Idempotency-Key") != "maktaba-7c1e" || f.bodies[i] != f.bodies[0] {
					t.Fatalf("attempt %d is another mail", i)
				}
			}
		})
	}
}

func TestResendStopsAfterItsTries(t *testing.T) {
	f := newFake(t, answer(502, ``))
	err := f.driver().Send(context.Background(), testMail)
	var se *SendError
	if !errors.As(err, &se) || !se.Temporary || se.Status != 502 || f.calls() != 3 {
		t.Fatalf("%v after %d calls", err, f.calls())
	}
}

// A 4xx is the mail's, the key's or the plan's: never tried again by the driver.
func TestResendDoesNotTryA4xxAgain(t *testing.T) {
	for _, tc := range []struct {
		status                    int
		name                      string
		temporary, refused, quota bool
	}{
		{400, "validation_error", false, false, false},
		{404, "not_found", false, false, false},
		{409, "invalid_idempotent_request", false, false, false},
		{422, "missing_required_field", false, false, false},
		{401, "missing_api_key", true, true, false},
		{403, "invalid_api_key", true, true, false},
		{403, "suspended_api_key", true, true, false},
		{429, "daily_quota_exceeded", true, false, true},
		{429, "monthly_quota_exceeded", true, false, true},
	} {
		f := newFake(t, answer(tc.status, `{"name":"`+tc.name+`","message":"you can't write to someone@example.test"}`))
		err := f.driver().Send(context.Background(), testMail)
		var se *SendError
		if !errors.As(err, &se) || f.calls() != 1 {
			t.Fatalf("%d %s: %v after %d calls", tc.status, tc.name, err, f.calls())
		}
		if se.Temporary != tc.temporary || se.Refused != tc.refused || se.Quota != tc.quota || se.Name != tc.name {
			t.Errorf("%d %s: %+v", tc.status, tc.name, se)
		}
		clean(t, err)
	}
}

// A malformed key answers 400: it is the key that is refused, and the mails wait. Another 400 is
// the mail's.
func TestResendMalformedKeyIsNotTheMailsFault(t *testing.T) {
	f := newFake(t, answer(400, `{"statusCode":400,"name":"validation_error","message":"API key is invalid"}`))
	var se *SendError
	if err := f.driver().Send(context.Background(), testMail); !errors.As(err, &se) || !se.Temporary || !se.Refused || f.calls() != 1 {
		t.Fatalf("%v (%d calls)", err, f.calls())
	}
}

// The provider's error name is kept only when it is one.
func TestResendErrorNames(t *testing.T) {
	for in, want := range map[string]string{"rate_limit_exceeded": "rate_limit_exceeded", "": "", "Someone@example.test": "",
		"a b": "", "x\ny": "", strings.Repeat("a", 64): strings.Repeat("a", 64), strings.Repeat("a", 65): ""} {
		if got := errorName(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// clean fails when an error names the recipient, the key, the subject or the link.
func clean(t *testing.T, err error) {
	t.Helper()
	for _, secret := range []string{"someone@", testKey, "SECRETTOKEN", "Hello", "write to"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error says %q: %v", secret, err)
		}
	}
}

func TestResendWaitsForRetryAfter(t *testing.T) {
	f := newFake(t, answer(429, `{"name":"rate_limit_exceeded"}`, "Retry-After", "1"), ok200)
	start := time.Now()
	if err := f.driver().Send(context.Background(), testMail); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < time.Second || f.calls() != 2 {
		t.Fatalf("tried again after %s, %d calls", took, f.calls())
	}
	// one longer than the driver waits goes back to the outbox, with the time
	f = newFake(t, answer(429, `{"name":"rate_limit_exceeded"}`, "Retry-After", "120"))
	err := f.driver().Send(context.Background(), testMail)
	var se *SendError
	if !errors.As(err, &se) || se.RetryAfter != 2*time.Minute || f.calls() != 1 {
		t.Fatalf("%v (%d calls)", err, f.calls())
	}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{"": 0, "-1": 0, "x": 0, "3": 3 * time.Second,
		"Wed, 25 Nov 2026 10:00:30 GMT": 30 * time.Second, "Wed, 25 Nov 2026 09:00:00 GMT": 0, "99999999": 24 * time.Hour} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("Retry-After %q: %s, want %s", v, got, want)
		}
	}
}

func TestResendTimesOut(t *testing.T) {
	stop := make(chan struct{})
	f := newFake(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	})
	defer close(stop)
	d := f.driver()
	d.Timeout, d.Tries = 50*time.Millisecond, 2
	err := d.Send(context.Background(), testMail)
	var se *SendError
	if !errors.As(err, &se) || se.Status != 0 || !se.Temporary || f.calls() != 2 {
		t.Fatalf("%v after %d calls", err, f.calls())
	}
	clean(t, err)
}

func TestResendStopsWhenCancelled(t *testing.T) {
	// waiting between two tries
	f := newFake(t, answer(429, `{}`, "Retry-After", "1"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := f.driver().Send(ctx, testMail); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("%v after %s", err, time.Since(start))
	}
	// in the middle of one
	stop := make(chan struct{})
	defer close(stop)
	f = newFake(t, func(http.ResponseWriter, *http.Request) { <-stop })
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if err := f.driver().Send(ctx, testMail); !errors.Is(err, context.Canceled) || f.calls() != 1 {
		t.Fatalf("%v (%d calls)", err, f.calls())
	}
}

func TestResendBoundsItsSends(t *testing.T) {
	var now atomic.Int32
	var mu sync.Mutex
	var most int32
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		most = max(most, now.Add(1))
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		now.Add(-1)
		ok200(w, r)
	})
	d := f.driver() // 2 at once
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.Send(context.Background(), testMail); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if most > 2 {
		t.Fatalf("%d at once", most)
	}
}

// The key goes to the one host: a redirect is not followed.
func TestResendFollowsNoRedirect(t *testing.T) {
	other := newFake(t, ok200)
	f := newFake(t, answer(307, ``, "Location", other.srv.URL+"/emails"))
	if err := f.driver().Send(context.Background(), testMail); err == nil {
		t.Fatal("a redirect is not a sent mail")
	}
	if other.calls() != 0 {
		t.Fatal("the redirect was followed")
	}
}

func TestResendWithoutAKeyOrAnID(t *testing.T) {
	f := newFake(t, ok200)
	d := f.driver()
	d.Key = func() (string, error) { return "", errors.New("no key") }
	var se *SendError
	if err := d.Send(context.Background(), testMail); !errors.As(err, &se) || !se.Refused || !se.Temporary {
		t.Fatalf("%v", err)
	}
	if err := f.driver().Send(context.Background(), Message{To: "a@example.test"}); err == nil {
		t.Fatal("a mail without an idempotency key")
	}
	if f.calls() != 0 {
		t.Fatal("it called anyway")
	}
}
