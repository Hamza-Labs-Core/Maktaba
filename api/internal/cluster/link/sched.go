package link

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// ErrClosed: the control channel is gone.
var ErrClosed = errors.New("link: the control channel is closed")

// sender is a control channel's outgoing queue (design §18.4): control frames always go
// before rpc frames, so a flood of proxied answers delays a command or a ticket by one
// message at most. An rpc sender blocks while its queue is full (back pressure on a big
// answer), never the control queue's. Data streams never pass here: each has its own
// connection. It keeps the longest a frame of each class waited for its write (the
// queueing a control frame meets, apart from the network and the peer).
type sender struct {
	control chan queued
	rpc     chan queued
	done    chan struct{}

	maxControlWait atomic.Int64 // nanoseconds
	maxRPCWait     atomic.Int64
}

type queued struct {
	b     []byte
	class byte
	at    time.Time
}

func newSender() *sender {
	return &sender{control: make(chan queued, 256), rpc: make(chan queued, 64), done: make(chan struct{})}
}

// send queues a frame; ctx bounds the wait for room.
func (s *sender) send(ctx context.Context, f Frame) error {
	b, err := f.Encode()
	if err != nil {
		return err
	}
	ch := s.rpc
	if f.Class == ClassControl {
		ch = s.control
	}
	select {
	case <-s.done:
		return ErrClosed
	default:
	}
	select {
	case ch <- queued{b: b, class: f.Class, at: time.Now()}:
		return nil
	case <-s.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// next is the next message to write: a control frame when one waits, else an rpc one.
func (s *sender) next() ([]byte, bool) {
	var q queued
	select {
	case q = <-s.control:
	default:
		select {
		case q = <-s.control:
		case q = <-s.rpc:
		case <-s.done:
			return nil, false
		}
	}
	waited := int64(time.Since(q.at))
	slot := &s.maxRPCWait
	if q.class == ClassControl {
		slot = &s.maxControlWait
	}
	for {
		old := slot.Load()
		if waited <= old || slot.CompareAndSwap(old, waited) {
			break
		}
	}
	return q.b, true
}

func (s *sender) close() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// writeLoop writes the queue to c until the sender closes or a write fails.
func (s *sender) writeLoop(c *websocket.Conn) error {
	for {
		b, ok := s.next()
		if !ok {
			return ErrClosed
		}
		wctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := c.Write(wctx, websocket.MessageBinary, b)
		cancel()
		if err != nil {
			return err
		}
	}
}
