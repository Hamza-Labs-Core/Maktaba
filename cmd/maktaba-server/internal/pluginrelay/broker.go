package pluginrelay

import (
	"errors"
	"io"
	"time"
)

// Broker hands callers to a listener that may not listen (story 54.23 on Windows): a
// first-party plugin runner, inside its AppContainer, may not create a named pipe and its
// Python has no AF_UNIX, so it serves the worker by dialing the supervisor instead. Each of
// its connections waits in the broker (Offer); a caller (the worker, through its own pipe,
// or the supervisor's health check) is paired with one waiting connection (Take, Serve):
// the runner reads Ready (a caller is here) and opens the next waiting connection, the
// caller reads Ready (a runner took it), and from then on the bytes pass unchanged, as
// through the plugin host's relay.
//
// A connection whose runner went away is found when Ready cannot be written to it and is
// dropped; with none waiting for Wait, the caller is closed without Ready, the way a
// refused unix socket ends a connect on the other systems.
type Broker struct {
	idle chan io.ReadWriteCloser
	wait time.Duration
}

// MaxWaiting bounds the connections a runner keeps waiting in the supervisor.
const MaxWaiting = 16

// ErrNoRunner: no runner connection waited (it is not up, or not serving).
var ErrNoRunner = errors.New("pluginrelay: no runner is waiting")

// NewBroker is a broker whose callers wait up to wait for a runner connection.
func NewBroker(wait time.Duration) *Broker {
	return &Broker{idle: make(chan io.ReadWriteCloser, MaxWaiting), wait: wait}
}

// Offer keeps a runner's waiting connection; one beyond MaxWaiting is closed.
func (b *Broker) Offer(c io.ReadWriteCloser) {
	select {
	case b.idle <- c:
	default:
		_ = c.Close()
	}
}

// Take is one waiting runner connection, told a caller is here (Ready written to it).
func (b *Broker) Take() (io.ReadWriteCloser, error) {
	deadline := time.NewTimer(b.wait)
	defer deadline.Stop()
	for {
		select {
		case c := <-b.idle:
			if _, err := c.Write([]byte{Ready}); err != nil {
				_ = c.Close() // its runner went away while it waited
				continue
			}
			return c, nil
		case <-deadline.C:
			return nil, ErrNoRunner
		}
	}
}

// Serve pairs one caller with a runner: Ready to the caller, then both ways until either
// side ends. Without a runner the caller is closed without Ready.
func (b *Broker) Serve(caller io.ReadWriteCloser) {
	r, err := b.Take()
	if err != nil {
		_ = caller.Close()
		return
	}
	if _, err := caller.Write([]byte{Ready}); err != nil {
		_ = caller.Close()
		_ = r.Close()
		return
	}
	Pump(caller, r)
}
