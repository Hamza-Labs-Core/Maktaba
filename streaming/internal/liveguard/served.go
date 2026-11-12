package liveguard

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// What the guard served a restricted viewer of a channel, kept until the API has
// counted it (Epic 57 story 57.14). The count is the stream's own: every request the
// guard lets through, and every second a continuous stream is written, marks the
// viewer as served now, and the time between two marks is owed when it is no longer
// than ServedGap. A longer silence is a viewer who was away, and is not counted.
//
// An HLS player asks for the playlist and a segment every few seconds, so its marks
// are seconds apart. ServedGap is a minute so that a player holding a long buffer is
// still counted whole, and so that a client fetching in bursts to stay under it
// would have to go without a full minute of video each time.
//
// The same channel on two devices of one account is one viewer on one channel here,
// and is counted once: the stream's credential names the account, not the device.

// ServedGap is the longest time between two served requests that is counted as
// watching.
const ServedGap = 60 * time.Second

// bookMax bounds the viewers and channels kept.
const bookMax = 4096

// errBookFull refuses a stream that could not be counted.
var errBookFull = errors.New("liveguard: too many viewers to count; the stream is refused")

type watchKey struct{ user, channel uuid.UUID }

type watched struct {
	last time.Time
	owed time.Duration
}

type book struct {
	mu sync.Mutex
	m  map[watchKey]*watched
}

// served marks k as served at now. It reports false when k cannot be kept: a stream
// that cannot be counted is not served.
func (b *book) served(k watchKey, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.m[k]
	if w == nil {
		if b.m == nil {
			b.m = map[watchKey]*watched{}
		}
		if len(b.m) >= bookMax {
			b.prune(now)
			if len(b.m) >= bookMax {
				return false
			}
		}
		b.m[k] = &watched{last: now}
		return true
	}
	if gap := now.Sub(w.last); gap > 0 {
		if gap <= ServedGap {
			w.owed += gap
		}
		w.last = now
	}
	return true
}

// prune drops the entries that owe nothing and were not served for ServedGap. The
// caller holds the lock.
func (b *book) prune(now time.Time) {
	for k, w := range b.m {
		if w.owed < time.Second && now.Sub(w.last) > ServedGap {
			delete(b.m, k)
		}
	}
}

// take removes and returns the whole seconds owed for k.
func (b *book) take(k watchKey) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.m[k]
	if w == nil {
		return 0
	}
	d := w.owed.Truncate(time.Second)
	w.owed -= d
	return d
}

// giveBack returns time the API did not take.
func (b *book) giveBack(k watchKey, d time.Duration) {
	if d <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if w := b.m[k]; w != nil {
		w.owed += d
		return
	}
	if b.m == nil {
		b.m = map[watchKey]*watched{}
	}
	b.m[k] = &watched{owed: d}
}

// owing lists the other channels user is owed a second or more for, and drops what
// is idle.
func (b *book) owing(user, except uuid.UUID, now time.Time) []uuid.UUID {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []uuid.UUID
	for k, w := range b.m {
		switch {
		case k.user != user || k.channel == except:
		case w.owed >= time.Second:
			out = append(out, k.channel)
		case now.Sub(w.last) > ServedGap:
			delete(b.m, k)
		}
	}
	return out
}
