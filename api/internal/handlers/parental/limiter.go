package parental

import (
	"sync"
	"time"
)

// limiter counts wrong tries per key in this process's memory: limit of them
// inside window close the key for hold. Nothing is stored, so an address is never
// written down; a restart forgets the counts (the stored limits, per guardian and
// per child, do not).
type limiter struct {
	limit        int
	window, hold time.Duration

	mu   sync.Mutex
	keys map[string]*tries
}

type tries struct {
	wrong []time.Time
	until time.Time
}

const limiterMaxKeys = 10000

func newLimiter(limit int, window, hold time.Duration) *limiter {
	return &limiter{limit: limit, window: window, hold: hold, keys: map[string]*tries{}}
}

// closedFor is how long key stays closed (0: it is open).
func (l *limiter) closedFor(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.keys[key]; ok && t.until.After(now) {
		return t.until.Sub(now)
	}
	return 0
}

// wrong counts one wrong try, and reports whether it closed the key.
func (l *limiter) wrong(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.keys) >= limiterMaxKeys {
		for k, t := range l.keys {
			if !t.until.After(now) && (len(t.wrong) == 0 || now.Sub(t.wrong[len(t.wrong)-1]) > l.window) {
				delete(l.keys, k)
			}
		}
		if len(l.keys) >= limiterMaxKeys {
			// full of live keys: start over rather than grow. The stored limits still hold.
			l.keys = map[string]*tries{}
		}
	}
	t := l.keys[key]
	if t == nil {
		t = &tries{}
		l.keys[key] = t
	}
	kept := t.wrong[:0]
	for _, at := range t.wrong {
		if now.Sub(at) <= l.window {
			kept = append(kept, at)
		}
	}
	t.wrong = append(kept, now)
	if len(t.wrong) >= l.limit {
		t.wrong = nil
		t.until = now.Add(l.hold)
		return true
	}
	return false
}

// clear forgets key's wrong tries (a right one).
func (l *limiter) clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}
