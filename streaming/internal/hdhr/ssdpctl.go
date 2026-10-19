package hdhr

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultSSDPPoll is how often the controller re-reads hdhr_device.
const DefaultSSDPPoll = 10 * time.Second

// SSDPController keeps the SSDP responder in step with the settings:
// advertising while hdhr_device.enabled is on AND this host is the
// configured streaming host (so two machines never announce the same
// DeviceID), silent otherwise. An enable or disable in the admin UI
// applies within one poll, no restart needed.
type SSDPController struct {
	Repo Repo
	// Allowed reports whether this host may advertise (tv.stream_host is
	// empty or names it). Nil: always.
	Allowed func(ctx context.Context) bool
	// LocationBase is the LOCATION base advertised (MAKTABA_PUBLIC_BASE_URL).
	LocationBase string
	Interval     time.Duration
	Logger       *slog.Logger
	// Serve runs the responder until ctx ends (nil: Responder.Run).
	Serve func(ctx context.Context, deviceUUID string) error

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	uuid   string

	errMu   sync.Mutex // not mu: Sync holds mu while it waits for a responder to exit
	lastErr string
}

// Running reports whether the responder is on.
func (c *SSDPController) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runningLocked()
}

func (c *SSDPController) runningLocked() bool {
	if c.done == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// Sync checks the device once and starts or stops the responder.
func (c *SSDPController) Sync(ctx context.Context) bool {
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	dev, err := c.Repo.Device(rctx)
	cancel()
	if err != nil {
		return c.Running() // keep what runs; retry at the next poll
	}
	want := dev.Enabled && (c.Allowed == nil || c.Allowed(ctx))

	c.mu.Lock()
	defer c.mu.Unlock()
	running := c.runningLocked()
	switch {
	case want && running && c.uuid == dev.UUID:
		return true
	case !want && !running:
		return false
	}
	if running {
		c.cancel()
		<-c.done
		c.done, c.cancel = nil, nil
		c.log().Info("hdhr: SSDP responder stopped", "event", "hdhr_ssdp_off")
	}
	if !want {
		return false
	}
	sctx, scancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.cancel, c.done, c.uuid = scancel, done, dev.UUID
	serve := c.Serve
	if serve == nil {
		base := c.LocationBase
		serve = func(ctx context.Context, id string) error {
			return (&Responder{DeviceUUID: id, LocationBase: base}).Run(ctx)
		}
	}
	go func() {
		defer close(done)
		if err := serve(sctx, dev.UUID); err != nil {
			c.errMu.Lock()
			repeat := err.Error() == c.lastErr
			c.lastErr = err.Error()
			c.errMu.Unlock()
			if !repeat {
				c.log().Warn("hdhr ssdp responder stopped", "err", err.Error(), "event", "hdhr_ssdp_stopped")
			}
		}
	}()
	c.errMu.Lock()
	retry := c.lastErr != ""
	c.errMu.Unlock()
	if !retry {
		c.log().Info("hdhr: SSDP responder started", "event", "hdhr_ssdp_started")
	}
	return true
}

// Run polls until ctx ends, then stops the responder.
func (c *SSDPController) Run(ctx context.Context) {
	iv := c.Interval
	if iv <= 0 {
		iv = DefaultSSDPPoll
	}
	c.Sync(ctx)
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Stop()
			return
		case <-t.C:
			c.Sync(ctx)
		}
	}
}

// Stop turns the responder off.
func (c *SSDPController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		<-c.done
		c.done, c.cancel = nil, nil
	}
}

func (c *SSDPController) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
