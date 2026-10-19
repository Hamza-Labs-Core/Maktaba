package spike

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

// clock is a fake clock that the fake sleep advances.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
	return nil
}

// fakePusher makes the fake account's input live (a reconnect within the
// input's timeout keeps the video id, a longer drop makes a new one).
type fakePusher struct {
	f        *cftest.Server
	clk      *clock
	timeout  time.Duration
	targets  []string
	lastStop time.Time
	video    string
	input    string
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *fakePusher) Start(_ context.Context, target string) (func() error, error) {
	p.targets = append(p.targets, target)
	p.f.Mu.Lock()
	for uid := range p.f.Inputs {
		if strings.Contains(target, "/"+p.f.Inputs[uid].RTMPS.StreamKey) {
			p.input = uid
		}
	}
	if p.video == "" || p.clk.Now().Sub(p.lastStop) > p.timeout {
		if p.video != "" { // the old broadcast becomes a recording
			v := p.f.Videos[p.video]
			v.Status.State = "ready"
		}
		p.video = newID()
		v := &cf.Video{UID: p.video, LiveInput: p.input}
		v.Status.State = "live-inprogress"
		v.Playback.HLS = "https://customer-" + p.f.CustomerCode + ".cloudflarestream.com/" + p.video + "/manifest/video.m3u8"
		p.f.Videos[p.video] = v
		p.f.StorageMinutes++
	}
	p.f.Lives[p.input] = cf.Lifecycle{IsInput: true, Live: true, VideoUID: p.video}
	p.f.Mu.Unlock()
	return func() error {
		p.f.Mu.Lock()
		defer p.f.Mu.Unlock()
		p.f.Lives[p.input] = cf.Lifecycle{IsInput: true}
		if v := p.f.Videos[p.video]; v != nil && !strings.Contains(target, "stopped") {
			v.Status.State = "ready"
		}
		p.lastStop = p.clk.Now()
		return nil
	}, nil
}

func TestSpikeAgainstFake(t *testing.T) {
	f := cftest.New(t)
	clk := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	f.Now = clk.Now
	f.SegmentExp = true
	p := &fakePusher{f: f, clk: clk, timeout: 30 * time.Second}
	rep, err := Run(context.Background(), Options{
		Client: f.Client("acc1", "tok-secret"), Duration: 60 * time.Second,
		Drops: []time.Duration{10 * time.Second, 60 * time.Second}, TimeoutSeconds: 30,
		LimitProbe: 3, Pusher: p, HTTP: f.Server.Client(), Now: clk.Now, Sleep: clk.Sleep,
	})
	if err != nil {
		t.Fatalf("run: %v (errors %v)", err, rep.Errors)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("errors: %v", rep.Errors)
	}
	if rep.CustomerCode != "abc123xyz" || rep.VideoUID == "" || rep.InputUID == "" {
		t.Errorf("report ids %+v", rep)
	}
	if !rep.TokenVideoSub.OK() || rep.TokenInputSub.OK() || rep.Unsigned.OK() {
		t.Errorf("(a) video %d input %d unsigned %d", rep.TokenVideoSub.Status, rep.TokenInputSub.Status, rep.Unsigned.Status)
	}
	if rep.ExpPerSegment == nil || !*rep.ExpPerSegment {
		t.Errorf("(b) exp per segment = %v", rep.ExpPerSegment)
	}
	if rep.PDTLagSecs == nil || *rep.PDTLagSecs < 7 {
		t.Errorf("(c) PDT lag %v", rep.PDTLagSecs)
	}
	if len(rep.Drops) != 2 || !rep.Drops[0].SameVideo || rep.Drops[1].SameVideo {
		t.Errorf("(d) drops %+v", rep.Drops)
	}
	if rep.RecordingUID == "" || rep.StorageFreed == nil {
		t.Errorf("(e) recording %q freed %v", rep.RecordingUID, rep.StorageFreed)
	}
	if rep.InputLimitCreated != 3 {
		t.Errorf("(f) created %d", rep.InputLimitCreated)
	}
	if len(f.Inputs) != 0 || len(f.Keys) != 0 {
		t.Errorf("not cleaned up: inputs %d keys %d", len(f.Inputs), len(f.Keys))
	}
	if !strings.HasPrefix(p.targets[0], "rtmps://live.cloudflare.com:443/live/key-") {
		t.Errorf("push target %q", p.targets[0])
	}
}

func TestFFmpegArgs(t *testing.T) {
	args := strings.Join(FFmpegPusher{}.Args("rtmps://live.cloudflare.com:443/live/KEY"), " ")
	for _, want := range []string{"testsrc2", "-c:v libx264", "-g 60", "-bf 0", "-c:a aac", "-f flv"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	srt := strings.Join(FFmpegPusher{Source: "ch.ts"}.Args("srt://live.cloudflare.com:778?streamid=x"), " ")
	if !strings.Contains(srt, "-f mpegts srt://") || !strings.Contains(srt, "-stream_loop -1 -i ch.ts") {
		t.Errorf("srt args %s", srt)
	}
}

func TestRedactKeys(t *testing.T) {
	got := redactKeys(`{"url":"rtmp://a","streamKey":"SECRET","x":{"passphrase": "PP"}}`)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "PP\"") {
		t.Errorf("redact: %s", got)
	}
}

func TestEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if _, _, err := Env(get); err == nil {
		t.Error("missing credentials must fail")
	}
	env["MAKTABA_CF_ACCOUNT_ID"], env["MAKTABA_CF_API_TOKEN"] = "a", "t"
	if a, tok, err := Env(get); err != nil || a != "a" || tok != "t" {
		t.Errorf("%q %q %v", a, tok, err)
	}
}
