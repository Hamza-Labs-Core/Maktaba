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
	"strconv"
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
	// Time is set when the viewer's time rules refuse playback now (Epic 57 story
	// 57.14): the stream is refused with the parental-time problem, whatever the
	// programme. Nil: the time rules allow it.
	Time *TimeRefusal `json:"-"`
	// TimeEndsAt is when a stream running now must end by the time rules; nil when
	// nothing ends it. The verdict is asked again then.
	TimeEndsAt *time.Time `json:"-"`
}

// TimeRefusal is why the time rules refuse, as the API's parental-time problem
// says it: Reason is outside_slot or limit_reached.
type TimeRefusal struct {
	Reason        string
	NextAllowedAt *time.Time
}

// Question is what the guard asks the API about one viewer and one channel.
type Question struct {
	User, Channel uuid.UUID
	// Family and Ceiling are the viewer's sign-in as the stream's credential
	// carries it: the refresh-token family ("" when it has none) and the age limit
	// its device gave (nil when none). The API holds the answer to that device's
	// limit as well as to the account's policy.
	Family  string
	Ceiling *int
	// Served is how long the guard served the viewer this channel since it last
	// said so, in whole seconds: the API counts it toward the viewer's time rules.
	Served time.Duration
}

// Verdicts answers whether a viewer may watch a channel now. An error is never an
// allowance: the guard refuses. An answer means the API counted q.Served.
type Verdicts interface {
	Verdict(ctx context.Context, q Question) (Verdict, error)
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
func (a *APIVerdicts) Verdict(ctx context.Context, ask Question) (Verdict, error) {
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
	q := url.Values{"channel": {ask.Channel.String()}, "user": {ask.User.String()}}
	if ask.Family != "" {
		q.Set("family", ask.Family)
	}
	if ask.Ceiling != nil {
		q.Set("ac", strconv.Itoa(*ask.Ceiling))
	}
	if secs := int(ask.Served / time.Second); secs > 0 {
		q.Set("served", strconv.Itoa(secs))
	}
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
		Restricted        *bool      `json:"restricted"`
		ChannelAllowed    *bool      `json:"channel_allowed"`
		Allowed           *bool      `json:"allowed"`
		Until             *time.Time `json:"until"`
		TimeAllowed       *bool      `json:"time_allowed"`
		TimeReason        string     `json:"time_reason"`
		TimeNextAllowedAt *time.Time `json:"time_next_allowed_at"`
		TimeEndsAt        *time.Time `json:"time_ends_at"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return Verdict{}, fmt.Errorf("liveguard: the parental verdict is unreadable: %w", err)
	}
	// time_allowed too: an API that does not say whether the time rules allow the
	// stream has not said that they do
	if v.Restricted == nil || v.ChannelAllowed == nil || v.Allowed == nil || v.TimeAllowed == nil {
		return Verdict{}, errors.New("liveguard: the parental verdict is incomplete")
	}
	out := Verdict{Restricted: *v.Restricted, ChannelAllowed: *v.ChannelAllowed, Allowed: *v.Allowed, Until: v.Until,
		TimeEndsAt: v.TimeEndsAt}
	if !*v.TimeAllowed {
		out.Time = &TimeRefusal{Reason: v.TimeReason, NextAllowedAt: v.TimeNextAllowedAt}
	}
	return out, nil
}

// VerdictTTL is the longest a verdict is reused: a rule a guardian changes reaches a
// running stream within it. A verdict is never reused past its Until (the programme
// boundary).
const VerdictTTL = 15 * time.Second

// verdictCacheMax bounds the cache.
const verdictCacheMax = 4096

// verdictKey is one viewer on one channel through one sign-in: the answer differs by
// the sign-in's device limit, so two sign-ins of one account do not share it.
type verdictKey struct {
	user, channel uuid.UUID
	family        string
	ceiling       int // noCeiling when the sign-in carries none
}

// noCeiling is verdictKey.ceiling for a sign-in with no device limit (no tier is
// this low: the limits are -1, 0, 9, 13, 16, 18).
const noCeiling = -1 << 31

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

// reuse is how long v stands from now: VerdictTTL, cut at the verdict's Until and
// at the moment the time rules end the stream.
func reuse(v Verdict, now time.Time) time.Duration {
	d := VerdictTTL
	for _, at := range []*time.Time{v.Until, v.TimeEndsAt} {
		if at != nil {
			if left := at.Sub(now); left < d {
				d = left
			}
		}
	}
	if d < 0 {
		d = 0
	}
	return d
}
