package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Error codes a refused or failed call answers with.
const (
	CodeOff           = "relay_off"
	CodeUnknown       = "unknown_extension"
	CodeHomeOnly      = "home_only"
	CodeNotAllowed    = "not_allowed"
	CodeBadRequest    = "bad_request"
	CodeNotConfigured = "not_configured"
	CodeNotMember     = "not_member"
	CodeRateLimited   = "rate_limited"
	CodeBusy          = "busy"
	CodeTooLarge      = "too_large"
	CodeTimeout       = "timeout"
	CodeUnreachable   = "upstream_unreachable"
	CodeCancelled     = "cancelled"
)

// Limits on what a call may carry and how long it may wait for a turn.
const (
	MaxRequestBody = 64 << 10
	DefaultChunk   = 512 << 10
	cacheTTL       = 5 * time.Second
	defaultRecent  = 200
)

// How long a call may wait for its turn (variables so tests can shorten them).
var (
	RateWaitMax = 5 * time.Second
	SlotWaitMax = 15 * time.Second
)

// Settings are the rules section "relay" (app_settings rules.relay); the
// defaults are the pipeline's RelayConfig.
type Settings struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute"`
	Burst             int  `json:"burst"`
	MaxInFlight       int  `json:"max_in_flight"`
	MaxResponseMB     int  `json:"max_response_mb"`
	TimeoutSec        int  `json:"timeout_sec"`
}

// DefaultSettings are used when the settings cannot be read.
func DefaultSettings() Settings {
	return Settings{Enabled: true, RequestsPerMinute: 600, Burst: 60, MaxInFlight: 4, MaxResponseMB: 256, TimeoutSec: 120}
}

// SettingsFrom reads the effective values of the rules section (unknown or
// mistyped values keep the default).
func SettingsFrom(m map[string]any) Settings {
	s := DefaultSettings()
	if v, ok := m["enabled"].(bool); ok {
		s.Enabled = v
	}
	num := func(k string, dst *int) {
		switch v := m[k].(type) {
		case float64:
			if v >= 1 {
				*dst = int(v)
			}
		case int:
			if v >= 1 {
				*dst = v
			}
		case json.Number:
			if n, err := v.Int64(); err == nil && n >= 1 {
				*dst = int(n)
			}
		}
	}
	num("requests_per_minute", &s.RequestsPerMinute)
	num("burst", &s.Burst)
	num("max_in_flight", &s.MaxInFlight)
	num("max_response_mb", &s.MaxResponseMB)
	num("timeout_sec", &s.TimeoutSec)
	return s
}

// Upstream is an extension's server as the home has it configured.
type Upstream struct {
	Enabled bool
	URL     string
	Secret  string
}

// Caller is the process a request came from (the agent session).
type Caller struct {
	Kind     string // "node" | "home"
	NodeID   string
	NodeName string
	Session  string
	WorkerID string
	IP       string
}

func (c Caller) key() string {
	if c.NodeID != "" {
		return "node:" + c.NodeID
	}
	return "home:" + c.WorkerID
}

// Request is one relayed call.
type Request struct {
	Ext         string
	Method      string
	Target      string // "/path?query"
	Accept      string
	ContentType string
	Body        []byte
	Timeout     time.Duration
}

// Sink receives the answer's body, in order.
type Sink interface {
	Chunk(ctx context.Context, data []byte) error
}

// Error is a refused or failed call.
type Error struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

func fail(code, detail string) *Error { return &Error{Code: code, Detail: detail} }

// Result is how a call ended.
type Result struct {
	Status      int
	ContentType string
	Bytes       int64
	Err         *Error
}

// Call is the audit record of one call: who, what, how it went. Never the
// secret, never the query (titles and keys are not needed to audit it).
type Call struct {
	TS       time.Time `json:"ts"`
	Kind     string    `json:"kind"`
	NodeID   string    `json:"node_id,omitempty"`
	NodeName string    `json:"node_name,omitempty"`
	Session  string    `json:"session,omitempty"`
	WorkerID string    `json:"worker_id,omitempty"`
	IP       string    `json:"ip,omitempty"`
	Ext      string    `json:"ext"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Status   int       `json:"status"`
	Bytes    int64     `json:"bytes"`
	MS       int64     `json:"ms"`
	Error    string    `json:"error,omitempty"`
	Scrubbed int       `json:"scrubbed,omitempty"`
}

// Config wires a Door.
type Config struct {
	// Settings reads rules.relay; nil: the defaults.
	Settings func(ctx context.Context) (Settings, error)
	// Upstream reads an extension's address and secret on the home.
	Upstream func(ctx context.Context, ext *Extension) (Upstream, error)
	// Member refuses a caller that is not (or no longer) an approved
	// machine; nil accepts every caller.
	Member func(ctx context.Context, c Caller) error
	// Client makes the upstream calls (redirects are never followed).
	Client *http.Client
	// Audit receives every call (the hub's events file).
	Audit      func(Call)
	Extensions map[string]*Extension
	Chunk      int
	Recent     int
	Now        func() time.Time
	Logger     *slog.Logger
}

type limiter struct {
	lim        *rate.Limiter
	rpm, burst int
	slots      chan struct{}
}

type cached[T any] struct {
	at  time.Time
	val T
	err error
}

// Door is the service door.
type Door struct {
	cfg    Config
	client *http.Client

	mu       sync.Mutex
	limiters map[string]*limiter
	settings cached[Settings]
	ups      map[string]cached[Upstream]
	recent   []Call
	totals   map[string]*Totals
}

// Totals counts a caller's calls since the API started.
type Totals struct {
	Calls   int   `json:"calls"`
	Refused int   `json:"refused"`
	Failed  int   `json:"failed"`
	Bytes   int64 `json:"bytes"`
}

// New builds a door.
func New(cfg Config) *Door {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Extensions == nil {
		cfg.Extensions = Extensions
	}
	if cfg.Chunk <= 0 {
		cfg.Chunk = DefaultChunk
	}
	if cfg.Recent <= 0 {
		cfg.Recent = defaultRecent
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 0,
		}}
	}
	c := *client
	// a redirect would take the secret header elsewhere: never followed
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Door{cfg: cfg, client: &c, limiters: map[string]*limiter{}, ups: map[string]cached[Upstream]{},
		totals: map[string]*Totals{}}
}

func (d *Door) now() time.Time { return d.cfg.Now() }

// Settings are the current settings (cached a few seconds).
func (d *Door) Settings(ctx context.Context) Settings {
	d.mu.Lock()
	c := d.settings
	d.mu.Unlock()
	if !c.at.IsZero() && d.now().Sub(c.at) < cacheTTL {
		return c.val
	}
	s := DefaultSettings()
	if d.cfg.Settings != nil {
		got, err := d.cfg.Settings(ctx)
		if err != nil {
			d.cfg.Logger.Warn("relay: settings unreadable, defaults used", "err", err, "event", "relay_settings_failed")
		} else {
			s = got
		}
	}
	d.mu.Lock()
	d.settings = cached[Settings]{at: d.now(), val: s}
	d.mu.Unlock()
	return s
}

func (d *Door) upstream(ctx context.Context, ext *Extension) (Upstream, error) {
	d.mu.Lock()
	c, ok := d.ups[ext.Name]
	d.mu.Unlock()
	if ok && d.now().Sub(c.at) < cacheTTL {
		return c.val, c.err
	}
	if d.cfg.Upstream == nil {
		return Upstream{}, errors.New("no upstream configured")
	}
	up, err := d.cfg.Upstream(ctx, ext)
	d.mu.Lock()
	d.ups[ext.Name] = cached[Upstream]{at: d.now(), val: up, err: err}
	d.mu.Unlock()
	return up, err
}

// Forget drops the cached settings and upstreams (a setting was saved).
func (d *Door) Forget() {
	d.mu.Lock()
	d.settings = cached[Settings]{}
	d.ups = map[string]cached[Upstream]{}
	d.mu.Unlock()
}

func (d *Door) limiterFor(key string, s Settings) *limiter {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := d.limiters[key]
	if l == nil || l.rpm != s.RequestsPerMinute || l.burst != s.Burst || cap(l.slots) != s.MaxInFlight {
		l = &limiter{lim: rate.NewLimiter(rate.Limit(float64(s.RequestsPerMinute)/60), s.Burst),
			rpm: s.RequestsPerMinute, burst: s.Burst, slots: make(chan struct{}, s.MaxInFlight)}
		d.limiters[key] = l
	}
	return l
}

var acceptRe = regexp.MustCompile(`^[A-Za-z0-9./+*,;= -]{1,128}$`)

// Serve runs one call: checks, the secret added, the answer streamed to
// sink with the secret scrubbed out. It always returns how it ended and
// records it.
func (d *Door) Serve(ctx context.Context, c Caller, req Request, sink Sink) (res Result) {
	start := d.now()
	path, _, _ := strings.Cut(req.Target, "?")
	if len(path) > 256 {
		path = path[:256]
	}
	scrubbed := 0
	defer func() {
		call := Call{TS: start.UTC(), Kind: c.Kind, NodeID: c.NodeID, NodeName: c.NodeName, Session: c.Session,
			WorkerID: c.WorkerID, IP: c.IP, Ext: req.Ext, Method: strings.ToUpper(req.Method), Path: path,
			Status: res.Status, Bytes: res.Bytes, MS: d.now().Sub(start).Milliseconds(), Scrubbed: scrubbed}
		if res.Err != nil {
			call.Error = res.Err.Code
		}
		d.record(c, call, res)
	}()
	s := d.Settings(ctx)
	if !s.Enabled {
		return Result{Err: fail(CodeOff, "the extension relay is switched off on the home")}
	}
	if why, ok := HomeOnly[req.Ext]; ok {
		return Result{Err: fail(CodeHomeOnly, why)}
	}
	ext := d.cfg.Extensions[req.Ext]
	if ext == nil {
		return Result{Err: fail(CodeUnknown, "no relayable extension "+clip(req.Ext, 40))}
	}
	if d.cfg.Member != nil {
		if err := d.cfg.Member(ctx, c); err != nil {
			return Result{Err: fail(CodeNotMember, "this machine is not an approved member")}
		}
	}
	if len(req.Body) > MaxRequestBody {
		return Result{Err: fail(CodeTooLarge, fmt.Sprintf("request body over %d bytes", MaxRequestBody))}
	}
	rule, upath, rawQuery, err := ext.Match(req.Method, req.Target)
	if err != nil {
		code := CodeNotAllowed
		if errors.Is(err, ErrBadTarget) {
			code = CodeBadRequest
		}
		return Result{Err: fail(code, strings.ToUpper(clip(req.Method, 10))+" "+clip(path, 120)+": "+err.Error())}
	}

	lim := d.limiterFor(c.key(), s)
	r := lim.lim.ReserveN(d.now(), 1)
	if !r.OK() || r.DelayFrom(d.now()) > RateWaitMax {
		r.Cancel()
		return Result{Err: fail(CodeRateLimited, fmt.Sprintf("over %d calls a minute", s.RequestsPerMinute))}
	}
	if wait := r.DelayFrom(d.now()); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			r.Cancel()
			return Result{Err: fail(CodeCancelled, "")}
		case <-t.C:
		}
	}
	slotT := time.NewTimer(SlotWaitMax)
	select {
	case lim.slots <- struct{}{}:
		slotT.Stop()
	case <-slotT.C:
		return Result{Err: fail(CodeBusy, fmt.Sprintf("%d calls of this machine already running", s.MaxInFlight))}
	case <-ctx.Done():
		slotT.Stop()
		return Result{Err: fail(CodeCancelled, "")}
	}
	defer func() { <-lim.slots }()

	up, err := d.upstream(ctx, ext)
	if err != nil || !up.Enabled || up.URL == "" || up.Secret == "" {
		why := "the extension is not set up on the home"
		if err == nil && !up.Enabled && up.URL != "" {
			why = "the extension is switched off on the home"
		}
		return Result{Err: fail(CodeNotConfigured, why)}
	}
	base, err := baseURL(up.URL)
	if err != nil {
		return Result{Err: fail(CodeNotConfigured, "the extension's address is not usable")}
	}

	timeout := time.Duration(s.TimeoutSec) * time.Second
	if req.Timeout > 0 && req.Timeout < timeout {
		timeout = req.Timeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	accept := "application/json"
	if acceptRe.MatchString(req.Accept) {
		accept = req.Accept
	}

	if rule.Verify == VerifyCollection {
		if e := d.verifyCollection(cctx, ext, base, upath, up.Secret); e != nil {
			return Result{Err: e}
		}
	}

	target := base + upath
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(cctx, rule.Method, target, body)
	if err != nil {
		return Result{Err: fail(CodeBadRequest, "request not buildable")}
	}
	hreq.Header.Set("Accept", accept)
	if len(req.Body) > 0 && acceptRe.MatchString(req.ContentType) {
		hreq.Header.Set("Content-Type", req.ContentType)
	}
	hreq.Header.Set(ext.Header, up.Secret)
	resp, err := d.client.Do(hreq)
	if err != nil {
		return Result{Err: d.transportErr(cctx, ctx, ext, up.Secret, err)}
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	res.ContentType = ScrubString(ext.QueryParam, up.Secret, clip(resp.Header.Get("Content-Type"), 128))

	sc := NewScrubber(ext.QueryParam, up.Secret)
	limit := int64(s.MaxResponseMB) << 20
	buf := make([]byte, d.cfg.Chunk)
	var read int64
	for {
		n, rerr := io.ReadFull(resp.Body, buf)
		if n > 0 {
			read += int64(n)
			if read > limit {
				res.Err = fail(CodeTooLarge, fmt.Sprintf("answer over %d MB", s.MaxResponseMB))
				break
			}
			if out := sc.Feed(buf[:n]); len(out) > 0 {
				if err := sink.Chunk(ctx, out); err != nil {
					res.Err = fail(CodeCancelled, "")
					break
				}
				res.Bytes += int64(len(out))
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			res.Err = d.transportErr(cctx, ctx, ext, up.Secret, rerr)
			break
		}
	}
	if res.Err == nil {
		if out := sc.Flush(); len(out) > 0 {
			if err := sink.Chunk(ctx, out); err != nil {
				res.Err = fail(CodeCancelled, "")
			}
			res.Bytes += int64(len(out))
		}
	}
	scrubbed = sc.Hits
	return res
}

func (d *Door) transportErr(cctx, ctx context.Context, ext *Extension, secret string, err error) *Error {
	switch {
	case ctx.Err() != nil:
		return fail(CodeCancelled, "")
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return fail(CodeTimeout, "the server did not answer in time")
	}
	return fail(CodeUnreachable, clip(ScrubString(ext.QueryParam, secret, err.Error()), 200))
}

// verifyCollection checks that the rating key at the end of path is a
// collection (a DELETE of any other item could remove a library item).
func (d *Door) verifyCollection(ctx context.Context, ext *Extension, base, path, secret string) *Error {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return fail(CodeBadRequest, "request not buildable")
	}
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set(ext.Header, secret)
	resp, err := d.client.Do(hreq)
	if err != nil {
		return fail(CodeUnreachable, clip(ScrubString(ext.QueryParam, secret, err.Error()), 200))
	}
	defer resp.Body.Close()
	var doc struct {
		MediaContainer struct {
			Metadata []struct {
				Type string `json:"type"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if resp.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&doc) != nil ||
		len(doc.MediaContainer.Metadata) != 1 || doc.MediaContainer.Metadata[0].Type != "collection" {
		return fail(CodeNotAllowed, "DELETE "+clip(path, 120)+": only a collection may be deleted")
	}
	return nil
}

func baseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("bad url")
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

func (d *Door) record(c Caller, call Call, res Result) {
	d.mu.Lock()
	d.recent = append(d.recent, call)
	if len(d.recent) > d.cfg.Recent {
		d.recent = append([]Call(nil), d.recent[len(d.recent)-d.cfg.Recent:]...)
	}
	t := d.totals[c.key()]
	if t == nil {
		t = &Totals{}
		d.totals[c.key()] = t
	}
	t.Calls++
	t.Bytes += res.Bytes
	switch {
	case res.Err != nil && res.Status == 0:
		t.Refused++
	case res.Err != nil || res.Status >= 400:
		t.Failed++
	}
	d.mu.Unlock()
	if d.cfg.Audit != nil {
		d.cfg.Audit(call)
	}
}

// ExtView is one extension on the Machines page.
type ExtView struct {
	Name       string      `json:"name"`
	Configured bool        `json:"configured"`
	Enabled    bool        `json:"enabled"`
	Upstream   string      `json:"upstream,omitempty"` // host:port only
	Reason     string      `json:"reason,omitempty"`
	Allow      []AllowView `json:"allow"`
}

// HomeOnlyView is an extension the door never relays.
type HomeOnlyView struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// CallerTotals is one caller's counters.
type CallerTotals struct {
	Caller string `json:"caller"`
	Totals
}

// Status is GET /api/cluster/relay's answer.
type Status struct {
	Available  bool           `json:"available"`
	Enabled    bool           `json:"enabled"`
	Settings   Settings       `json:"settings"`
	Extensions []ExtView      `json:"extensions"`
	HomeOnly   []HomeOnlyView `json:"home_only"`
	Calls      []Call         `json:"calls"`
	Totals     []CallerTotals `json:"totals"`
}

// Status reports the door (never a secret): settings, the extensions and
// whether each is set up, the recent calls newest first.
func (d *Door) Status(ctx context.Context) Status {
	s := d.Settings(ctx)
	out := Status{Available: true, Enabled: s.Enabled, Settings: s, Extensions: []ExtView{}, HomeOnly: []HomeOnlyView{},
		Calls: []Call{}, Totals: []CallerTotals{}}
	names := make([]string, 0, len(d.cfg.Extensions))
	for n := range d.cfg.Extensions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ext := d.cfg.Extensions[n]
		v := ExtView{Name: n, Allow: ext.Allowed()}
		up, err := d.upstream(ctx, ext)
		switch {
		case err != nil:
			v.Reason = "settings unreadable"
		case up.URL == "":
			v.Reason = "no server address"
		case up.Secret == "":
			v.Reason = "no secret stored on the home"
		default:
			v.Configured = true
		}
		v.Enabled = err == nil && up.Enabled
		if u, perr := url.Parse(up.URL); perr == nil && up.URL != "" {
			v.Upstream = u.Host
		}
		out.Extensions = append(out.Extensions, v)
	}
	for n, why := range HomeOnly {
		out.HomeOnly = append(out.HomeOnly, HomeOnlyView{Name: n, Reason: why})
	}
	sort.Slice(out.HomeOnly, func(i, j int) bool { return out.HomeOnly[i].Name < out.HomeOnly[j].Name })
	d.mu.Lock()
	for i := len(d.recent) - 1; i >= 0; i-- {
		out.Calls = append(out.Calls, d.recent[i])
	}
	for k, t := range d.totals {
		out.Totals = append(out.Totals, CallerTotals{Caller: k, Totals: *t})
	}
	d.mu.Unlock()
	sort.Slice(out.Totals, func(i, j int) bool { return out.Totals[i].Caller < out.Totals[j].Caller })
	return out
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// SetMember sets the membership check (the hub's: an approved machine with
// a live session). Call it before the door serves.
func (d *Door) SetMember(fn func(ctx context.Context, c Caller) error) { d.cfg.Member = fn }

// SetAudit adds an audit receiver (the hub's events file) to the
// configured one. Call it before the door serves.
func (d *Door) SetAudit(fn func(Call)) {
	prev := d.cfg.Audit
	d.cfg.Audit = func(c Call) {
		if prev != nil {
			prev(c)
		}
		fn(c)
	}
}
