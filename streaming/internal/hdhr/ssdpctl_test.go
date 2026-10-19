package hdhr

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type togglingRepo struct {
	mu  sync.Mutex
	dev Device
}

func (r *togglingRepo) Device(context.Context) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dev, nil
}
func (r *togglingRepo) Lineup(context.Context) ([]LineupChannel, error) { return nil, nil }

func (r *togglingRepo) set(f func(*Device)) {
	r.mu.Lock()
	f(&r.dev)
	r.mu.Unlock()
}

func TestSSDPController_FollowsEnabledAndStreamHost(t *testing.T) {
	repo := &togglingRepo{dev: Device{UUID: "u-1", Enabled: false}}
	var serving atomic.Int32
	var lastUUID atomic.Value
	allowed := atomic.Bool{}
	allowed.Store(true)
	c := &SSDPController{
		Repo:    repo,
		Allowed: func(context.Context) bool { return allowed.Load() },
		Serve: func(ctx context.Context, id string) error {
			serving.Add(1)
			lastUUID.Store(id)
			<-ctx.Done()
			serving.Add(-1)
			return nil
		},
	}
	ctx := context.Background()
	if c.Sync(ctx) || serving.Load() != 0 {
		t.Fatal("advertising while disabled")
	}
	repo.set(func(d *Device) { d.Enabled = true }) // enabled in the admin UI
	if !c.Sync(ctx) || !waitTrue(func() bool { return serving.Load() == 1 }) {
		t.Fatal("enable did not start SSDP")
	}
	if !c.Sync(ctx) || serving.Load() != 1 {
		t.Fatal("a second sync must not start a second responder")
	}
	repo.set(func(d *Device) { d.UUID = "u-2" }) // a new identity restarts it
	if !c.Sync(ctx) || !waitTrue(func() bool { return serving.Load() == 1 && lastUUID.Load() == "u-2" }) {
		t.Fatal("uuid change not applied")
	}
	allowed.Store(false) // another host is the streaming host
	if c.Sync(ctx) || serving.Load() != 0 {
		t.Fatal("advertising on a host that is not the streaming host")
	}
	allowed.Store(true)
	c.Sync(ctx)
	repo.set(func(d *Device) { d.Enabled = false }) // disabled
	if c.Sync(ctx) || serving.Load() != 0 || c.Running() {
		t.Fatal("disable did not stop SSDP")
	}
}

func TestSSDPController_RetriesAfterBindFailureAndStopsOnCancel(t *testing.T) {
	repo := &togglingRepo{dev: Device{UUID: "u", Enabled: true}}
	var calls atomic.Int32
	c := &SSDPController{Repo: repo, Interval: 20 * time.Millisecond,
		Serve: func(ctx context.Context, _ string) error {
			if calls.Add(1) == 1 {
				return context.DeadlineExceeded // port 1900 busy
			}
			<-ctx.Done()
			return nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	if !waitTrue(func() bool { return calls.Load() >= 2 && c.Running() }) {
		t.Fatal("not retried after the responder failed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	if c.Running() {
		t.Error("still running after cancel")
	}
}

func waitTrue(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
