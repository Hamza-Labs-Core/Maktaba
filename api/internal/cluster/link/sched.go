package link

import (
	"context"
	"errors"

	"github.com/coder/websocket"
)

// ErrClosed: the control channel is gone.
var ErrClosed = errors.New("link: the control channel is closed")

// sender is a control channel's outgoing queue (design §18.4): control frames always go
// before rpc frames, so a flood of proxied answers delays a command or a ticket by one
// message at most. An rpc sender blocks while its queue is full (back pressure on a big
// answer), never the control queue's.
type sender struct {
	control chan []byte
	rpc     chan []byte
	done    chan struct{}
}

func newSender() *sender {
	return &sender{control: make(chan []byte, 256), rpc: make(chan []byte, 64), done: make(chan struct{})}
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
	case ch <- b:
		return nil
	case <-s.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// next is the next message to write: a control frame when one waits, else an rpc one.
func (s *sender) next() ([]byte, bool) {
	select {
	case b := <-s.control:
		return b, true
	default:
	}
	select {
	case b := <-s.control:
		return b, true
	case b := <-s.rpc:
		return b, true
	case <-s.done:
		return nil, false
	}
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
