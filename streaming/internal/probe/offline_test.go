package probe

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// failingBackend is a database that is gone.
type failingBackend struct{ calls int }

func (f *failingBackend) Lookup(context.Context, uuid.UUID) (*Row, error) {
	f.calls++
	return nil, errors.New("probe: lookup video: dial tcp: connect: connection refused")
}

func TestOfflineBackend(t *testing.T) {
	a, missing := uuid.New(), uuid.New()
	home, cp := NewFakeBackend(), NewFakeBackend()
	home.Set(&Row{VideoID: a, Path: "/x/live.mp4", Probed: true})
	cp.Set(&Row{VideoID: a, Path: "/x/a.mp4", Probed: true})

	// the home's database answers: its answer stands, no-such-video included
	o := &Offline{Primary: home, Copy: cp}
	if r, err := o.Lookup(context.Background(), a); err != nil || r.Path != "/x/live.mp4" {
		t.Fatalf("the database answering: %+v %v", r, err)
	}
	if _, err := o.Lookup(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown video with the database answering: %v, want ErrNotFound", err)
	}

	// the home's database fails: the copy answers, the failure is recorded
	dead := &failingBackend{}
	var failed []error
	down := false
	o = &Offline{Primary: dead, Copy: cp, Down: func() bool { return down },
		Failed: func(err error) { failed = append(failed, err); down = true }}
	if r, err := o.Lookup(context.Background(), a); err != nil || r.Path != "/x/a.mp4" {
		t.Fatalf("the database failing: %+v %v", r, err)
	}
	if len(failed) != 1 {
		t.Fatalf("the failure was not recorded: %v", failed)
	}
	// known down: not asked at all; the copy's no-such-video is an answer
	if _, err := o.Lookup(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a video the copy does not have: %v, want ErrNotFound", err)
	}
	if dead.calls != 1 {
		t.Fatalf("a database known down was asked: %d calls", dead.calls)
	}
	// the copy gone too, or none
	o.Copy = &failingBackend{}
	if _, err := o.Lookup(context.Background(), a); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("neither answering: %v, want ErrUnavailable", err)
	}
	o.Copy = nil
	if _, err := o.Lookup(context.Background(), a); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no copy: %v, want ErrUnavailable", err)
	}
}
