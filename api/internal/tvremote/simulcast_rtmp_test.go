package tvremote_test

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/rtmptest"
)

// Story 32.13 against a fake RTMP destination (rtmptest): the fake Cloudflare
// plays the retransmission (an ffmpeg publisher to the output's url + key
// while the output sits on a live input), so the test sees what the
// destination sees. The URL and key Maktaba sends must make a publishable
// target; a failover leaves the destination's one publisher connected; a
// rotation is one short gap on the same output (a second publish, same key,
// no output re-created); deleting the output disconnects it.
//
// Skips without ffmpeg on PATH and in -short.
func TestSimulcastReachesRTMPDestination(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	rtmptest.NeedFFmpeg(t)
	dst, err := rtmptest.Listen(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dst.Close)
	fx := newSim(t)
	fx.rules(map[string]any{"max_push_hours": 1})
	var mu sync.Mutex
	targets := []string{}
	fx.f.Forward = func(u, key string) func() {
		target := strings.TrimRight(u, "/") + "/" + key
		mu.Lock()
		targets = append(targets, target)
		mu.Unlock()
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=15",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-c:v", "libx264", "-preset", "ultrafast",
			"-g", "15", "-bf", "0", "-c:a", "aac", "-f", "flv", target)
		if err := cmd.Start(); err != nil {
			t.Errorf("ffmpeg: %v", err)
			return func() {}
		}
		return func() {
			_ = cmd.Process.Kill() // only the PID this test started
			_, _ = cmd.Process.Wait()
		}
	}
	t.Cleanup(fx.f.StopForwards)
	const key = "sim-key-0123456789"
	waitActive := func(want int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for dst.Active(key) != want {
			if time.Now().After(deadline) {
				t.Fatalf("destination publishers %d, want %d (targets %v)", dst.Active(key), want, targets)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	publishes := func() int {
		n := 0
		for _, p := range dst.Publishes() {
			if p.Key == key {
				n++
			}
		}
		return n
	}

	ch := fx.chans[0]
	d := fx.dest(tvremote.KindRTMP, dst.URL(), key)
	att := fx.attach(ch, d.ID, tvremote.ModeAlways, nil)
	fx.exec(`INSERT INTO channel_placements (channel_id, host_id, generation, state) VALUES ($1, 'node-pc', 3, 'live')`, ch)
	fx.reconcile()
	first := fx.goLive(ch)
	uid := fx.cfInput(first.ID)
	fx.reconcile()
	waitActive(1)

	// failover: the relay switches its source, the input stays live: the
	// destination's publisher never drops
	fx.exec(`UPDATE channel_placements SET host_id = 'home', generation = 4, reason = 'failover' WHERE channel_id = $1`, ch)
	for i := 0; i < 3; i++ {
		fx.advance(20 * time.Second)
		fx.reconcile()
	}
	time.Sleep(500 * time.Millisecond)
	if dst.Active(key) != 1 || publishes() != 1 {
		t.Fatalf("after the failover: active %d, publishes %d", dst.Active(key), publishes())
	}

	// rotation: a new broadcast on the same input; the output is kept and
	// reconnects (one gap), never re-created
	fx.advance(time.Hour)
	fx.reconcile()
	fx.relay(first.ID, "stopped")
	fx.reconcile()
	fx.f.EndLive(uid)
	waitActive(0)
	fx.reconcile()
	fx.goLive(ch)
	fx.reconcile()
	waitActive(1)
	if publishes() != 2 || fx.postOutputs(uid) != 1 {
		t.Fatalf("rotation: publishes %d, output creates %d", publishes(), fx.postOutputs(uid))
	}
	if o := fx.open(att); o.State != tvremote.OutLive {
		t.Fatalf("output after the rotation %+v", o)
	}

	// Stop now: the output is deleted and the destination disconnects
	if err := fx.c.StopOutput(t.Context(), "admin-1", att); err != nil {
		t.Fatal(err)
	}
	fx.reconcile()
	waitActive(0)
	if fx.f.OutputCount() != 0 {
		t.Fatal("the output is still at Cloudflare")
	}
}
