package probe

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrUnavailable: neither the home's database nor this machine's copy of it answers.
var ErrUnavailable = errors.New("probe: neither the home's database nor this machine's copy of it answers")

// Offline is the Backend of a full node (specs/epics/37-node-replica): the home's
// database while it answers, else this machine's copy of it. An answer — no such
// video, not probed — is the home's; only a failure (the database gone, a timeout)
// goes to the copy.
type Offline struct {
	Primary Backend
	// Copy is this machine's copy of the home's database (nil: none).
	Copy Backend
	// Down: the home's database does not answer (skip it rather than wait); Failed
	// records a lookup that failed on it.
	Down    func() bool
	Failed  func(error)
	Timeout time.Duration // a lookup's bound (0: 3 s)
}

func (o *Offline) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 3 * time.Second
}

func answered(err error) bool {
	return err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotProbed)
}

// Lookup implements Backend.
func (o *Offline) Lookup(ctx context.Context, id uuid.UUID) (*Row, error) {
	if o.Down == nil || !o.Down() {
		qctx, cancel := context.WithTimeout(ctx, o.timeout())
		row, err := o.Primary.Lookup(qctx, id)
		cancel()
		if answered(err) || ctx.Err() != nil {
			return row, err
		}
		if o.Failed != nil {
			o.Failed(err)
		}
	}
	if o.Copy != nil {
		qctx, cancel := context.WithTimeout(ctx, o.timeout())
		row, err := o.Copy.Lookup(qctx, id)
		cancel()
		if answered(err) {
			return row, err
		}
	}
	return nil, ErrUnavailable
}
