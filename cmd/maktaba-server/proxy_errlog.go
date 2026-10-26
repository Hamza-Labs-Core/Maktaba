package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// proxyErrEvery is how often a reverse proxy's failure is logged per distinct error: a
// service that is down fails every request (a player's segments, the web app's polls), and
// one line per window says it as well as hundreds.
const proxyErrEvery = 30 * time.Second

// errThrottle lets one line per key through per window and counts the ones it held back.
type errThrottle struct {
	every time.Duration
	now   func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
	held map[string]int
}

func newErrThrottle(every time.Duration) *errThrottle {
	return &errThrottle{every: every, now: time.Now, last: map[string]time.Time{}, held: map[string]int{}}
}

// allow says whether key's line is logged now, and how many like it were held back since
// the last one that was.
func (t *errThrottle) allow(key string) (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if at, ok := t.last[key]; ok && now.Sub(at) < t.every {
		t.held[key]++
		return false, 0
	}
	if len(t.last) >= 256 {
		// many distinct errors: forget the ones whose window is over
		for k, at := range t.last {
			if now.Sub(at) >= t.every {
				delete(t.last, k)
				delete(t.held, k)
			}
		}
	}
	held := t.held[key]
	t.last[key], t.held[key] = now, 0
	return true, held
}

var (
	apiProxyErrs        = newErrThrottle(proxyErrEvery)
	streamProxyErrs     = newErrThrottle(proxyErrEvery)
	homeStreamProxyErrs = newErrThrottle(proxyErrEvery)
	authCheckErrs       = newErrThrottle(proxyErrEvery)
)

// logProxyError logs a reverse proxy's failure to reach its service ("[serve] level=warn
// api proxy: GET /api/x: …"): not a request whose client went away (its context
// canceled: nothing failed), and once per window per distinct error (t).
func logProxyError(out io.Writer, t *errThrottle, what string, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
		return
	}
	ok, held := t.allow(err.Error())
	if !ok {
		return
	}
	more := ""
	if held > 0 {
		more = fmt.Sprintf(" (%d more like it in the last %s)", held, t.every)
	}
	fmt.Fprintf(out, "[serve] level=warn %s: %s %s: %v%s\n", what, r.Method, r.URL.Path, err, more)
}

// stderrProxyError is logProxyError to serve's stderr.
func stderrProxyError(t *errThrottle, what string, r *http.Request, err error) {
	logProxyError(os.Stderr, t, what, r, err)
}
