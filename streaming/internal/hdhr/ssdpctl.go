package hdhr

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"
)

// DefaultSSDPPoll is how often the controller re-reads hdhr_device.
const DefaultSSDPPoll = 10 * time.Second

// SSDPController keeps the discovery responders (SSDP on udp/1900 and the
// HDHomeRun discover protocol on udp/65001) in step with the settings:
// advertising while hdhr_device.enabled is on AND this host is the
// configured streaming host (so two machines never announce the same
// DeviceID), silent otherwise. An enable or disable in the admin UI
// applies within one poll, no restart needed.
type SSDPController struct {
	Repo Repo
	// Allowed reports whether this host may advertise (tv.stream_host is
	// empty or names it). Nil: always.
	Allowed func(ctx context.Context) bool
	// LocationBase is MAKTABA_PUBLIC_BASE_URL: when set it is the advertised
	// base; else each client is told http://<our address on its
	// network>:<HTTPAddr's port> (Advertiser), never a relative URL.
	LocationBase string
	// HTTPAddr is the public listen address (MAKTABA_HTTP_ADDR, ":8081").
	HTTPAddr string
	// DeviceAuth is the token advertised in HDHomeRun discover replies
	// (the one /discover.json reports; empty: none).
	DeviceAuth string
	Interval   time.Duration
	Logger     *slog.Logger
	// Serve runs the responders until ctx ends (nil: ServeDiscovery, the SSDP
	// responder and the HDHomeRun UDP discover responder side by side).
	Serve func(ctx context.Context, dev Device) error
	// ListenSSDP / ListenDiscover open the sockets for the default Serve
	// (nil: ListenSSDP on udp/1900, ListenDiscover on udp/65001, all
	// interfaces). Tests inject loopback sockets.
	ListenSSDP     func() (net.PacketConn, error)
	ListenDiscover func() (net.PacketConn, error)
	// Multicast / Ifaces override the SSDP NOTIFY path (tests).
	Multicast func(on LocalIface, msg []byte) error
	Ifaces    func() []LocalIface
	// RetryEvery is how soon a responder that failed to bind retries
	// (0: 30 s).
	RetryEvery time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	key    string

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
	case want && running && c.key == deviceKey(dev):
		return true
	case !want && !running:
		return false
	}
	if running {
		c.cancel()
		<-c.done
		c.done, c.cancel = nil, nil
		c.log().Info("hdhr: discovery responders stopped", "event", "hdhr_ssdp_off")
	}
	if !want {
		return false
	}
	sctx, scancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.cancel, c.done, c.key = scancel, done, deviceKey(dev)
	serve := c.Serve
	if serve == nil {
		serve = c.ServeDiscovery
	}
	go func() {
		defer close(done)
		if err := serve(sctx, dev); err != nil {
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
		c.log().Info("hdhr: discovery responders (SSDP, HDHomeRun UDP 65001) started", "event", "hdhr_ssdp_started")
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

// deviceKey is what a running responder advertises: a change restarts it.
func deviceKey(d Device) string {
	return d.UUID + "|" + d.DeviceID + "|" + strconv.Itoa(d.TunerCount)
}

// ServeDiscovery runs the SSDP responder and the HDHomeRun UDP discover
// responder until ctx ends. Each binds its own port; one that fails (port
// busy, no multicast) is logged and retried without stopping the other.
func (c *SSDPController) ServeDiscovery(ctx context.Context, dev Device) error {
	adv := Advertiser{PublicBase: c.LocationBase, HTTPAddr: c.HTTPAddr}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.retry(ctx, "ssdp", func(ctx context.Context) error {
			listen := c.ListenSSDP
			if listen == nil {
				listen = ListenSSDP
			}
			conn, err := listen()
			if err != nil {
				return err
			}
			return (&Responder{Conn: conn, DeviceUUID: dev.UUID, Advertiser: adv,
				Multicast: c.Multicast, Ifaces: c.Ifaces}).Run(ctx)
		})
	}()
	go func() {
		defer wg.Done()
		c.retry(ctx, "discover", func(ctx context.Context) error {
			listen := c.ListenDiscover
			if listen == nil {
				listen = ListenDiscover
			}
			conn, err := listen()
			if err != nil {
				return err
			}
			return (&DiscoverResponder{Conn: conn, Device: dev, DeviceAuth: c.DeviceAuth,
				Advertiser: adv}).Run(ctx)
		})
	}()
	wg.Wait()
	return nil
}

// retry runs fn until ctx ends, restarting it RetryEvery after a failure and
// logging each distinct error once.
func (c *SSDPController) retry(ctx context.Context, name string, fn func(context.Context) error) {
	every := c.RetryEvery
	if every <= 0 {
		every = 30 * time.Second
	}
	last := ""
	for ctx.Err() == nil {
		err := fn(ctx)
		if ctx.Err() != nil {
			return
		}
		msg := "stopped"
		if err != nil {
			msg = err.Error()
		}
		if msg != last {
			c.log().Warn("hdhr: discovery responder failed; retrying", "responder", name, "err", msg,
				"retry_in", every.String(), "event", "hdhr_discovery_failed")
			last = msg
		}
		select {
		case <-ctx.Done():
		case <-time.After(every):
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
