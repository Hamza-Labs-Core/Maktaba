// Package clock provides an injectable time source so tests can pin
// "now" without monkey-patching time.Now.
package clock

import "time"

// Clock abstracts wall-clock reads. Production uses Real; tests can use
// a Fake that returns a fixed value.
type Clock interface {
	Now() time.Time
}

// Real returns time.Now() from the standard library.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

// Fake returns a fixed value; advance via Set.
type Fake struct{ t time.Time }

func NewFake(t time.Time) *Fake { return &Fake{t: t} }
func (f *Fake) Now() time.Time  { return f.t }
func (f *Fake) Set(t time.Time) { f.t = t }
func (f *Fake) Advance(d time.Duration) {
	f.t = f.t.Add(d)
}
