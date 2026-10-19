package frontdoor

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// The TS side of a swap (Epic 33 story 33.7 AC6, design §8.5): a client's
// connection — a Plex tuner, VLC, the push relay — reads one slot's live.ts.
// When the channel's switch is planned the target slot's live.ts is opened
// before the instant (make-before-break) and read alongside; at the first IDR
// of the live slot at or after the instant the copier looks for the target's
// IDR with the same PTS (story 33.4: both slots stamp wall-clock timestamps and
// put an IDR at every block start and on the keyframe grid), writes the
// target's program tables and continues from that IDR. The client sees one
// program: no closed connection, timestamps continuing (the retimer behind
// keeps the continuity counters and finds no offset to apply), audio from the
// first frame after the IDR. When no matching IDR shows up in time (a slot
// behind, a parameter change) the target joins at its newest IDR and the
// retimer evens the timestamps out (retimed).

// errChannelRemoved ends the TS of a channel the new lineup no longer has
// (story 33.7 AC8).
var errChannelRemoved = errors.New("frontdoor: the channel is not in the new lineup")

// errRouted: the channel was routed to another slot without a planned switch
// (a failover): the caller splices onto the new route (retimed).
var errRouted = errors.New("frontdoor: the channel was routed elsewhere")

const (
	// spliceTolerance: two IDRs this close (90 kHz) are the same instant
	// (aligned output: equal; a frame at 60 fps is 1500).
	spliceTolerance = 1500
	// spliceFallback: no matching IDR this long after the instant → retimed.
	spliceFallback = 6 * time.Second
	// spliceHold: how long the live slot's packets from the instant's IDR wait
	// for the target's IDR with the same PTS.
	spliceHold = time.Second
	// spliceWallSlack: an IDR this much before the instant (wall clock) still
	// counts as at it (half a frame, a little clock jitter).
	spliceWallSlack = 50 * time.Millisecond
	tsReadChunk     = 188 * 128
)

// tsUpstream is one slot's live.ts of a channel, read in its own goroutine
// into batches of whole packets.
type tsUpstream struct {
	name   string
	body   io.ReadCloser
	ctx    context.Context
	cancel context.CancelFunc
	pkts   chan []byte
	mu     sync.Mutex
	err    error
	once   sync.Once
}

func (u *tsUpstream) close() {
	u.once.Do(func() {
		u.cancel()
		_ = u.body.Close()
	})
}

func (u *tsUpstream) readErr() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err
}

func (u *tsUpstream) loop(count func(int64)) {
	defer close(u.pkts)
	r := bufio.NewReaderSize(u.body, 64<<10)
	buf := make([]byte, tsReadChunk)
	var carry []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			count(int64(n))
			b := append(carry, buf[:n]...)
			// keep packet alignment (resync on the sync byte)
			for len(b) > 0 && b[0] != 0x47 {
				b = b[1:]
			}
			whole := len(b) - len(b)%relay.PacketSize
			if whole > 0 {
				out := make([]byte, whole)
				copy(out, b[:whole])
				select {
				case u.pkts <- out:
				case <-u.ctx.Done():
					return
				}
			}
			carry = append([]byte(nil), b[whole:]...)
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			u.mu.Lock()
			u.err = err
			u.mu.Unlock()
			return
		}
	}
}

// openSlotTS opens a slot's live.ts of a channel.
func (h *Handler) openSlotTS(ctx context.Context, name string, ch uuid.UUID) (*tsUpstream, error) {
	uctx, cancel := context.WithCancel(ctx)
	resp, err := h.slotGet(uctx, name, "/stream/channel/"+ch.String()+"/live.ts", nil)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, &upstreamStatus{code: resp.StatusCode, status: resp.Status}
	}
	u := &tsUpstream{name: name, body: resp.Body, ctx: uctx, cancel: cancel, pkts: make(chan []byte, 512)}
	go u.loop(h.count("ts", "slot:"+name))
	return u, nil
}

type upstreamStatus struct {
	code   int
	status string
}

func (e *upstreamStatus) Error() string { return "frontdoor: TV slot answered " + e.status }

// pendingNext is the target of a planned switch, read ahead of the instant.
type pendingNext struct {
	u    *tsUpstream
	scan relay.Scanner
	pat  []byte
	pmt  []byte
	// buf: the target's packets since its second newest IDR (idrs: their
	// offsets in buf and PTS)
	buf  [][]byte
	idrs []idrMark
}

type idrMark struct {
	at  int
	pts int64
}

func (p *pendingNext) add(b []byte) {
	for off := 0; off+relay.PacketSize <= len(b); off += relay.PacketSize {
		pkt := b[off : off+relay.PacketSize]
		info := p.scan.Scan(pkt)
		switch {
		case info.PAT:
			p.pat = append(p.pat[:0], pkt...)
			continue
		case info.PMT:
			p.pmt = append(p.pmt[:0], pkt...)
			continue
		}
		if info.IDR && info.HasPTS {
			p.idrs = append(p.idrs, idrMark{at: len(p.buf), pts: info.PTS})
			if len(p.idrs) > 3 {
				// keep the last three GOPs (the live slot may be up to two behind)
				cut := p.idrs[len(p.idrs)-3].at
				p.buf = append([][]byte(nil), p.buf[cut:]...)
				keep := p.idrs[len(p.idrs)-3:]
				p.idrs = p.idrs[:0]
				for _, m := range keep {
					p.idrs = append(p.idrs, idrMark{at: m.at - cut, pts: m.pts})
				}
			}
		}
		if len(p.idrs) > 0 {
			p.buf = append(p.buf, pkt)
		}
	}
}

// match is the buffered IDR at pts (-1: none); behind reports that the target
// has not reached pts yet.
func (p *pendingNext) match(pts int64) (idx int, behind bool) {
	if len(p.idrs) == 0 || p.pat == nil || p.pmt == nil {
		return -1, true
	}
	for i, m := range p.idrs {
		d := relay.PTSDiff(m.pts, pts)
		if d >= -spliceTolerance && d <= spliceTolerance {
			return i, false
		}
	}
	return -1, relay.PTSDiff(p.idrs[len(p.idrs)-1].pts, pts) < 0
}

// spliceOut is the part of the target written at the switch: its tables, then
// its packets from IDR i on.
func (p *pendingNext) spliceOut(i int) []byte {
	out := make([]byte, 0, (len(p.buf)-p.idrs[i].at+2)*relay.PacketSize)
	out = append(out, p.pat...)
	out = append(out, p.pmt...)
	for _, pkt := range p.buf[p.idrs[i].at:] {
		out = append(out, pkt...)
	}
	return out
}

// slotTS copies the routed slot's TS of a channel to w until it ends, splicing
// onto the target slot at a planned switch (spliced is called with the new
// slot just before its first byte: the retimer behind is told a new source
// starts). It returns errRouted when the channel was moved without a plan (a
// failover: the caller resolves again) and errChannelRemoved at a removed
// channel's instant.
func (h *Handler) slotTS(ctx context.Context, ch uuid.UUID, w io.Writer, started, spliced func(name string)) error {
	cur := h.Slots.Route(ch)
	u, err := h.openSlotTS(ctx, cur, ch)
	if err != nil {
		var us *upstreamStatus
		if ctx.Err() == nil && (!errors.As(err, &us) || (us.code >= 500 && us.code != http.StatusServiceUnavailable)) {
			h.Slots.Suspect(ctx, cur)
		}
		return err
	}
	if started != nil {
		started(cur)
	}
	defer func() { u.close() }()
	var next *pendingNext
	var scan relay.Scanner
	var lastOpen time.Time
	// hold: the live slot reached the instant's IDR before the target did; its
	// packets from that IDR on wait (at most spliceHold) for the target's IDR
	// with the same PTS, so the splice is exactly there
	var held []byte
	var holdPTS int64
	var holdSince time.Time
	var holdPlan SwitchPlan
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	closeNext := func() {
		if next != nil {
			next.u.close()
			next = nil
		}
	}
	defer closeNext()
	flushHeld := func() error {
		if len(held) == 0 {
			return nil
		}
		_, err := w.Write(held)
		held = nil
		return err
	}
	// splice writes the target from its IDR i and makes it the current source
	splice := func(plan SwitchPlan, i int, mode string, at time.Time) error {
		held = nil
		if spliced != nil {
			spliced(plan.To)
		}
		if _, err := w.Write(next.spliceOut(i)); err != nil {
			return err
		}
		tvmetrics.TSSplices.Inc(mode)
		h.log().Info("tv: TS connection spliced onto the new slot", "channel_id", ch.String(), "from", cur,
			"to", plan.To, "mode", mode, "at", at.UTC().Format(time.RFC3339Nano),
			"late_ms", at.Sub(plan.At).Milliseconds(), "event", "tv_ts_splice")
		u.close()
		u, cur, scan = next.u, plan.To, next.scan
		next = nil
		return nil
	}
	var nextPkts <-chan []byte
	for {
		if next != nil {
			nextPkts = next.u.pkts
		} else {
			nextPkts = nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			plan, planned := h.Slots.Plan(ch)
			if planned && plan.From == cur && plan.To != cur {
				now := time.Now()
				if plan.Removed {
					if !now.Before(plan.At) {
						return errChannelRemoved
					}
					continue
				}
				if held != nil && now.Sub(holdSince) > spliceHold {
					if err := flushHeld(); err != nil { // the target is not coming: try the next IDR
						return err
					}
				}
				if next == nil && plan.At.Sub(now) <= swapPrepLead && now.Sub(lastOpen) >= time.Second {
					lastOpen = now
					if nu, err := h.openSlotTS(ctx, plan.To, ch); err == nil {
						next = &pendingNext{u: nu}
					}
				}
				if next != nil && held == nil && now.Sub(plan.At) > spliceFallback {
					if _, ok := next.retimedOut(); ok {
						h.log().Warn("tv: no matching IDR on the new slot; joining it retimed", "channel_id", ch.String(),
							"from", cur, "to", plan.To, "event", "tv_ts_splice_retimed")
						if err := splice(plan, len(next.idrs)-1, "retimed", now); err != nil {
							return err
						}
					}
				}
				continue
			}
			if err := flushHeld(); err != nil {
				return err
			}
			if r := h.Slots.Route(ch); r != cur {
				closeNext()
				return errRouted
			}
			closeNext()
		case b, ok := <-nextPkts:
			if !ok {
				closeNext() // the target's stream ended: opened again at the next tick
				if err := flushHeld(); err != nil {
					return err
				}
				continue
			}
			next.add(b)
			if held != nil {
				if i, _ := next.match(holdPTS); i >= 0 {
					if err := splice(holdPlan, i, "seamless", channel.PTSToWall(holdPTS, time.Now())); err != nil {
						return err
					}
				}
			}
		case b, ok := <-u.pkts:
			if !ok {
				err := u.readErr()
				if ctx.Err() == nil {
					if r := h.Slots.Route(ch); r == cur {
						h.Slots.Suspect(ctx, cur) // the slot's stream broke under a live client
					}
				}
				if err == nil && ctx.Err() == nil {
					err = errors.New("frontdoor: the slot's stream ended")
				}
				return err
			}
			if held != nil {
				held = append(held, b...)
				continue
			}
			plan, planned := h.Slots.Plan(ch)
			armed := planned && next != nil && plan.From == cur && plan.To != cur && !plan.Removed
			start := 0
			for off := 0; off+relay.PacketSize <= len(b); off += relay.PacketSize {
				info := scan.Scan(b[off : off+relay.PacketSize])
				if !armed || !info.IDR || !info.HasPTS {
					continue
				}
				wall := channel.PTSToWall(info.PTS, time.Now())
				if wall.Before(plan.At.Add(-spliceWallSlack)) {
					continue
				}
				i, behind := next.match(info.PTS)
				if i < 0 && !behind {
					continue // the target is past this IDR (or has none): the next one
				}
				// the live slot up to this IDR
				if off > start {
					if _, err := w.Write(b[start:off]); err != nil {
						return err
					}
				}
				if i < 0 {
					held, holdPTS, holdSince, holdPlan = append([]byte(nil), b[off:]...), info.PTS, time.Now(), plan
				} else if err := splice(plan, i, "seamless", wall); err != nil {
					return err
				}
				start = -1
				break
			}
			if start >= 0 && start < len(b) {
				if _, err := w.Write(b[start:]); err != nil {
					return err
				}
			}
		}
	}
}

// retimedOut is the target from its newest IDR (the fallback join).
func (p *pendingNext) retimedOut() ([]byte, bool) {
	if len(p.idrs) == 0 || p.pat == nil || p.pmt == nil {
		return nil, false
	}
	return p.spliceOut(len(p.idrs) - 1), true
}
