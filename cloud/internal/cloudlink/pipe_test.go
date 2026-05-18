package cloudlink

import (
	"errors"
	"sync"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// pipeConn is an in-memory FrameConn whose writes appear on the peer's
// reads. It satisfies BOTH cloudlink.FrameConn and relay.FrameConn (the
// signatures are identical) so the same pipe wires the real cloud-side
// relay.Tunnel to the real cloudlink.Multiplexer with no network and no
// second protocol implementation.
type pipeConn struct {
	in       chan Frame
	out      chan Frame
	closed   chan struct{} // this end's own close signal
	peerDone chan struct{} // the peer's close signal
	once     sync.Once
}

func newPipe() (*pipeConn, *pipeConn) {
	a2b := make(chan Frame, 64)
	b2a := make(chan Frame, 64)
	ac := make(chan struct{})
	bc := make(chan struct{})
	a := &pipeConn{in: b2a, out: a2b, closed: ac, peerDone: bc}
	b := &pipeConn{in: a2b, out: b2a, closed: bc, peerDone: ac}
	return a, b
}

func (p *pipeConn) ReadFrame() (Frame, error) {
	select {
	case f, ok := <-p.in:
		if !ok {
			return Frame{}, errors.New("pipe: closed")
		}
		return f, nil
	case <-p.closed:
		return Frame{}, errors.New("pipe: closed")
	case <-p.peerDone:
		return Frame{}, errors.New("pipe: peer closed")
	}
}

func (p *pipeConn) WriteFrame(f Frame) error {
	select {
	case p.out <- f:
		return nil
	case <-p.closed:
		return errors.New("pipe: closed")
	case <-p.peerDone:
		return errors.New("pipe: peer closed")
	}
}

func (p *pipeConn) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

// Compile-time proof that one pipe end satisfies both interfaces.
var (
	_ FrameConn       = (*pipeConn)(nil)
	_ relay.FrameConn = (*pipeConn)(nil)
)
