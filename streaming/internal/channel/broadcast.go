package channel

import (
	"io"
	"sync"
)

// tsPacket is the MPEG-TS packet size; the hub only ever forwards whole
// packets so a client joining mid-stream starts on a packet boundary.
const tsPacket = 188

// hubChunk is how many bytes the reader hands the hub at once.
const hubChunk = tsPacket * 56

// Hub fans one channel's continuous MPEG-TS out to any number of
// clients (HDHomeRun tuners, raw .ts pulls). A client that can't keep
// up (its buffer fills) is dropped rather than stalling the channel.
type Hub struct {
	mu     sync.Mutex
	subs   map[int]chan []byte
	next   int
	closed bool
	buf    int
}

// NewHub builds a hub; buf is the per-client queue length in chunks.
func NewHub(buf int) *Hub {
	if buf <= 0 {
		buf = 512 // ≈ 5 MB ≈ 7 s at 6 Mb/s
	}
	return &Hub{subs: map[int]chan []byte{}, buf: buf}
}

// Subscribe registers a client. The channel closes when the hub closes
// or the client is dropped for being too slow; cancel unsubscribes.
func (h *Hub) Subscribe() (<-chan []byte, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, h.buf)
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	id := h.next
	h.next++
	h.subs[id] = ch
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
	}
}

// Clients reports the current subscriber count.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Publish forwards a chunk to every client (non-blocking).
func (h *Hub) Publish(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.subs) == 0 {
		return
	}
	c := append([]byte(nil), b...)
	for id, ch := range h.subs {
		select {
		case ch <- c:
		default:
			delete(h.subs, id)
			close(ch)
		}
	}
}

// Close ends every client stream.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, ch := range h.subs {
		delete(h.subs, id)
		close(ch)
	}
}

// pump reads r in whole-packet chunks into the hub until EOF/error.
func (h *Hub) pump(r io.Reader) {
	buf := make([]byte, hubChunk)
	for {
		n, err := io.ReadFull(r, buf)
		if n >= tsPacket {
			h.Publish(buf[:n-n%tsPacket])
		}
		if err != nil {
			return
		}
	}
}

// packetWriter forwards only whole 188-byte packets to w, holding a
// partial tail until it completes; Discard drops a leftover tail when a
// block process exits mid-packet so the next block starts aligned.
type packetWriter struct {
	w    io.Writer
	tail []byte
	n    int64
}

func (p *packetWriter) Write(b []byte) (int, error) {
	total := len(b)
	if len(p.tail) > 0 {
		need := tsPacket - len(p.tail)
		if len(b) < need {
			p.tail = append(p.tail, b...)
			return total, nil
		}
		p.tail = append(p.tail, b[:need]...)
		b = b[need:]
		if _, err := p.w.Write(p.tail); err != nil {
			return 0, err
		}
		p.n += tsPacket
		p.tail = p.tail[:0]
	}
	whole := len(b) - len(b)%tsPacket
	if whole > 0 {
		if _, err := p.w.Write(b[:whole]); err != nil {
			return 0, err
		}
		p.n += int64(whole)
	}
	if rest := b[whole:]; len(rest) > 0 {
		p.tail = append(p.tail, rest...)
	}
	return total, nil
}

// Discard drops a partial trailing packet.
func (p *packetWriter) Discard() { p.tail = p.tail[:0] }

// Written is the number of whole-packet bytes forwarded.
func (p *packetWriter) Written() int64 { return p.n }
