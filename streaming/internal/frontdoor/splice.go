package frontdoor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// Splicing (design §4.2, §6.2).
const (
	// MaxSplices: how many times one client connection may move to a new upstream.
	MaxSplices = 3
	// spliceGiveUp: a client that got no bytes for this long is let go.
	spliceGiveUp = 25 * time.Second
	// tsWriteTimeout drops a TS client whose connection stalls.
	tsWriteTimeout = 15 * time.Second
	spliceRetry    = 500 * time.Millisecond
)

// clientWriter writes to the client (deadline per write, flush per write) and
// remembers a write failure (the client is gone: never splice for it).
type clientWriter struct {
	w     io.Writer
	fl    http.Flusher
	rc    *http.ResponseController
	n     int64
	err   error
	count func(int64)
}

func newClientWriter(w io.Writer, count func(int64)) *clientWriter {
	cw := &clientWriter{w: w, count: count}
	cw.fl, _ = w.(http.Flusher)
	if rw, ok := w.(http.ResponseWriter); ok {
		cw.rc = http.NewResponseController(rw)
	}
	return cw
}

func (c *clientWriter) Write(b []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.rc != nil {
		_ = c.rc.SetWriteDeadline(time.Now().Add(tsWriteTimeout))
	}
	n, err := c.w.Write(b)
	c.n += int64(n)
	if err != nil {
		c.err = err
		return n, err
	}
	if c.fl != nil {
		c.fl.Flush()
	}
	return n, nil
}

// Flush satisfies http.Flusher (the engine's pump flushes after each write).
func (c *clientWriter) Flush() {}

// StreamChannelTS writes the channel's continuous MPEG-TS to w until ctx ends
// (satisfies hdhr.Streamer: the tuner lease stays on the home for the whole client
// connection). When the source ends or fails while the client is still there it
// resolves again — a dead host's channel is re-placed — and continues on the same
// connection with the new host's TS, up to MaxSplices times (AC4 of 32.4).
func (h *Handler) StreamChannelTS(ctx context.Context, ch uuid.UUID, w io.Writer) error {
	detach := h.Tracker.AttachTS(ch)
	defer detach()
	cw := newClientWriter(w, nil)
	if cw.fl != nil {
		cw.fl.Flush() // the client gets its 200 at once, before the first packet
	}
	sources := 0
	lastData := time.Now()
	var lastErr error
	for {
		if ctx.Err() != nil {
			return nil
		}
		before := cw.n
		p, err := h.Svc.Resolve(ctx, ch)
		if err == nil {
			if p.Host == h.Self {
				err = h.Engine.StreamChannelTS(ctx, ch, cw)
			} else {
				err = h.remoteTS(ctx, p.Host, p.Gen, ch, cw)
			}
		}
		if cw.err != nil || ctx.Err() != nil {
			return cw.err // the client went away
		}
		if cw.n > before {
			sources++
			lastData = time.Now()
			if sources > 1 {
				h.log().Info("tv: tuner connection spliced onto a new source", "channel_id", ch.String(),
					"host_id", p.Host, "splice", sources-1, "event", "tv_splice")
			}
			if sources > MaxSplices {
				return errors.New("frontdoor: splices exhausted")
			}
		}
		if err != nil {
			lastErr = err
		}
		h.Svc.Invalidate(ch)
		if time.Since(lastData) > spliceGiveUp {
			if lastErr == nil {
				lastErr = errors.New("frontdoor: stream ended")
			}
			return lastErr
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(h.retryDelay()):
		}
	}
}

func (h *Handler) retryDelay() time.Duration {
	if h.SpliceRetry > 0 {
		return h.SpliceRetry
	}
	return spliceRetry
}

// remoteTS copies a node's live.ts to the client until it ends.
func (h *Handler) remoteTS(ctx context.Context, hostID string, gen int64, ch uuid.UUID, cw *clientWriter) error {
	host, ok := h.Svc.Hosts.Get(hostID)
	base := strings.TrimRight(host.BaseURL, "/")
	if !ok || base == "" {
		h.Svc.Hosts.Fail(hostID)
		return errors.New("frontdoor: host address unknown")
	}
	path := "/stream/channel/" + ch.String() + "/live.ts"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	h.Keyring.SignRequest(req, gen)
	resp, err := h.client().Do(req)
	if err != nil {
		if ctx.Err() == nil {
			h.Svc.Hosts.Fail(hostID)
			tvmetrics.Errors.Inc(hostID, "ts")
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 500 && resp.StatusCode != http.StatusServiceUnavailable {
			h.Svc.Hosts.Fail(hostID)
			tvmetrics.Errors.Inc(hostID, "ts")
		}
		return errors.New("frontdoor: node answered " + resp.Status)
	}
	h.Svc.Hosts.OK(hostID)
	n, err := copyFlush(cw, resp.Body)
	h.count("ts", hostID)(n)
	if cw.err == nil && ctx.Err() == nil && h.stillPlaced(ctx, ch, hostID, gen) {
		// the node's stream broke under a live client: a failure of that host
		h.Svc.Hosts.Fail(hostID)
	}
	return err
}

// stillPlaced reports whether the channel's placement still names host with gen. A
// node ends a live.ts on purpose when the channel moved or stopped (its station is
// fenced): that is no failure of the node, and counting it would mark a healthy
// node down after three TS clients of one moved channel (a read error counts as
// still placed).
func (h *Handler) stillPlaced(ctx context.Context, ch uuid.UUID, hostID string, gen int64) bool {
	p, found, err := h.Svc.Store.Get(ctx, ch)
	if err != nil {
		return true
	}
	return found && p.Active() && p.Host == hostID && p.Gen == gen
}
