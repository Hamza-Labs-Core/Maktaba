package liveguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Verdict is the API's parental answer for one viewer and one channel, now
// (GET /api/tv/parental/verdict, api/internal/handlers/parental/livetv.go).
type Verdict struct {
	// Restricted: the viewer has a parental limit.
	Restricted bool `json:"restricted"`
	// ChannelAllowed: the viewer may see the channel at all.
	ChannelAllowed bool `json:"channel_allowed"`
	// Allowed: the viewer may watch the programme now on the channel.
	Allowed bool `json:"allowed"`
	// Until is when the answer may change (the next programme that flips it); nil when
	// nothing in the API's horizon does.
	Until *time.Time `json:"until"`
}

// Verdicts answers whether a viewer may watch a channel now. An error is never an
// allowance: the guard refuses.
type Verdicts interface {
	Verdict(ctx context.Context, user, channel uuid.UUID) (Verdict, error)
}

// LocalTokenHeader carries this machine's local token to the API
// (api/internal/auth/middleware.LocalTokenHeader).
const LocalTokenHeader = "X-Maktaba-Local-Token"

// VerdictTimeout bounds one question to the API: a slow answer is a refusal.
const VerdictTimeout = 3 * time.Second

// APIVerdicts asks the home API on loopback, with this machine's local token.
type APIVerdicts struct {
	// BaseURL is the API's origin (MAKTABA_LINK_PULL_URL); "" answers every question
	// with an error.
	BaseURL string
	// TokenFile is the local token's file (MAKTABA_SUPERVISOR_TOKEN_FILE).
	TokenFile string
	Client    *http.Client
}

// APIVerdictsFromEnv is the client of the supervisor's environment.
func APIVerdictsFromEnv() *APIVerdicts {
	return &APIVerdicts{BaseURL: strings.TrimRight(os.Getenv("MAKTABA_LINK_PULL_URL"), "/"),
		TokenFile: os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"),
		Client:    &http.Client{Timeout: VerdictTimeout}}
}

// ErrNoAPI: this process was given no API to ask.
var ErrNoAPI = errors.New("liveguard: no API to ask the parental verdict (MAKTABA_LINK_PULL_URL, MAKTABA_SUPERVISOR_TOKEN_FILE)")

// Verdict implements Verdicts.
func (a *APIVerdicts) Verdict(ctx context.Context, user, channel uuid.UUID) (Verdict, error) {
	if a == nil || a.BaseURL == "" || a.TokenFile == "" {
		return Verdict{}, ErrNoAPI
	}
	raw, err := os.ReadFile(a.TokenFile)
	tok := strings.TrimSpace(string(raw))
	if err != nil || tok == "" {
		return Verdict{}, ErrNoAPI
	}
	ctx, cancel := context.WithTimeout(ctx, VerdictTimeout)
	defer cancel()
	q := url.Values{"channel": {channel.String()}, "user": {user.String()}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+"/api/tv/parental/verdict?"+q.Encode(), nil)
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set(LocalTokenHeader, tok)
	req.Header.Set("Accept", "application/json")
	c := a.Client
	if c == nil {
		c = &http.Client{Timeout: VerdictTimeout}
	}
	resp, err := c.Do(req)
	if err != nil {
		return Verdict{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return Verdict{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Verdict{}, fmt.Errorf("liveguard: the parental verdict answered %s", resp.Status)
	}
	// every field must be there: a body that is not the verdict (a proxy's page, an
	// older API) allows nothing
	var v struct {
		Restricted     *bool      `json:"restricted"`
		ChannelAllowed *bool      `json:"channel_allowed"`
		Allowed        *bool      `json:"allowed"`
		Until          *time.Time `json:"until"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return Verdict{}, fmt.Errorf("liveguard: the parental verdict is unreadable: %w", err)
	}
	if v.Restricted == nil || v.ChannelAllowed == nil || v.Allowed == nil {
		return Verdict{}, errors.New("liveguard: the parental verdict is incomplete")
	}
	return Verdict{Restricted: *v.Restricted, ChannelAllowed: *v.ChannelAllowed, Allowed: *v.Allowed, Until: v.Until}, nil
}

// VerdictTTL is the longest a verdict is reused: a rule a guardian changes reaches a
// running stream within it. A verdict is never reused past its Until (the programme
// boundary).
const VerdictTTL = 15 * time.Second

// verdictCacheMax bounds the cache.
const verdictCacheMax = 4096

type verdictKey struct{ user, channel uuid.UUID }

type cachedVerdict struct {
	v     Verdict
	until time.Time
}

// cache is the short reuse of verdicts: per viewer and channel, until the sooner of
// VerdictTTL and the verdict's own Until. Errors are never cached.
type cache struct {
	mu sync.Mutex
	m  map[verdictKey]cachedVerdict
}

func (c *cache) get(k verdictKey, now time.Time) (Verdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || !now.Before(e.until) {
		return Verdict{}, false
	}
	return e.v, true
}

func (c *cache) put(k verdictKey, v Verdict, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= verdictCacheMax {
		c.m = map[verdictKey]cachedVerdict{}
	}
	c.m[k] = cachedVerdict{v: v, until: now.Add(reuse(v, now))}
}

// reuse is how long v stands from now: VerdictTTL, cut at the verdict's Until.
func reuse(v Verdict, now time.Time) time.Duration {
	d := VerdictTTL
	if v.Until != nil {
		if left := v.Until.Sub(now); left < d {
			d = left
		}
	}
	if d < 0 {
		d = 0
	}
	return d
}
