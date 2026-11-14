package broadcast

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// Event is one message of the edge's control socket (GET /v1/ingest/ws): hello,
// rung_stalled, on_air, off_air, revoked, restored, plan_off, pong; and the on-demand
// wake and sleep, and limits (a rung the plan's mode removed).
type Event struct {
	Type   string `json:"type"`
	Rung   string `json:"rung,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Rungs is a limits event's rungs (top first).
	Rungs []string `json:"rungs,omitempty"`
}

// Control keeps one channel's control socket to the edge: outbound, reconnected with
// jitter (1 s doubling to 30 s), pinged every 20 s.
type Control struct {
	Creds func() (Creds, bool)
	HTTP  *http.Client
	Now   func() time.Time
	// On hears each event; a closed socket is no event.
	On func(Event)
	// Ping is the ping period (default 20 s).
	Ping time.Duration
}

func wsURL(ingest string) string {
	u := strings.TrimRight(ingest, "/") + "/v1/ingest/ws"
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

// Run connects until ctx ends.
func (c *Control) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if c.session(ctx) {
			backoff = time.Second
		}
		wait := time.Duration(float64(backoff) * (0.5 + rand.Float64())) //nolint:gosec // reconnect jitter
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(2*backoff, 30*time.Second)
	}
}

// session runs one connection; it reports whether it got as far as a hello.
func (c *Control) session(ctx context.Context) bool {
	cr, ok := c.Creds()
	if !ok {
		return false
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+cr.Token)
	h.Set("X-Maktaba-Time", strconv.FormatInt(now().UnixMilli(), 10))
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, wsURL(cr.IngestURL), &websocket.DialOptions{HTTPHeader: h, HTTPClient: c.HTTP})
	cancel()
	if err != nil {
		return false
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(64 << 10)
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		p := c.Ping
		if p <= 0 {
			p = 20 * time.Second
		}
		t := time.NewTicker(p)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				if conn.Write(sctx, websocket.MessageText, []byte("ping")) != nil {
					stop()
					return
				}
			}
		}
	}()
	hello := false
	for {
		_, msg, err := conn.Read(sctx)
		if err != nil {
			return hello
		}
		var ev Event
		if json.Unmarshal(msg, &ev) != nil || ev.Type == "" {
			continue
		}
		if ev.Type == "hello" {
			hello = true
		}
		if ev.Type != "pong" && c.On != nil {
			c.On(ev)
		}
	}
}

// Controls keeps a control socket per published channel the Ctl knows, and hands their
// events to it (the catalog's streaming:broadcast_control).
type Controls struct {
	Ctl  *Ctl
	HTTP *http.Client
	// Poll is how often the channels are compared (default 5 s).
	Poll time.Duration
}

// Run follows the Ctl's channels until ctx ends.
func (s *Controls) Run(ctx context.Context) {
	type conn struct {
		token  string
		cancel context.CancelFunc
	}
	open := map[uuid.UUID]conn{}
	defer func() {
		for _, c := range open {
			c.cancel()
		}
	}()
	poll := s.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		seen := map[uuid.UUID]bool{}
		for _, j := range s.Ctl.Jobs() {
			if j.State == "draft" || j.Creds.Token == "" {
				continue
			}
			seen[j.Channel] = true
			if c, ok := open[j.Channel]; ok && c.token == j.Creds.Token {
				continue
			} else if ok {
				c.cancel() // a new token: reconnect with it
			}
			cctx, cancel := context.WithCancel(ctx)
			ch := j.Channel
			ctl := &Control{HTTP: s.HTTP, Now: s.Ctl.Now,
				Creds: func() (Creds, bool) { return s.Ctl.creds(ch) },
				On:    func(ev Event) { s.Ctl.Event(cctx, ch, ev) }}
			go ctl.Run(cctx)
			open[ch] = conn{token: j.Creds.Token, cancel: cancel}
		}
		for ch, c := range open {
			if !seen[ch] {
				c.cancel()
				delete(open, ch)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
