package rtmptest

import (
	"os/exec"
	"testing"
	"time"
)

// ffmpeg publishes to the fake over RTMPS and RTMP (skips without ffmpeg, and in -short).
func TestFFmpegPublishes(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	NeedFFmpeg(t)
	for _, tlsOn := range []bool{true, false} {
		s, err := Listen(tlsOn)
		if err != nil {
			t.Fatal(err)
		}
		got := make(chan string, 1)
		s.OnPublish = func(k string) { got <- k }
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "2", "-c:v", "libx264", "-preset", "ultrafast",
			"-g", "30", "-bf", "0", "-c:a", "aac", "-f", "flv", s.URL()+"thekey")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg (tls %v): %v\n%s", tlsOn, err, out)
		}
		select {
		case k := <-got:
			if k != "thekey" {
				t.Errorf("key %q", k)
			}
		case <-time.After(time.Second):
			t.Fatal("no publish")
		}
		time.Sleep(100 * time.Millisecond)
		ps := s.Publishes()
		if len(ps) != 1 {
			t.Fatalf("publishes %d", len(ps))
		}
		var last uint32
		video := 0
		for _, tg := range ps[0].Tags {
			if tg.Type == 9 && !tg.SeqHeader {
				if video > 0 && tg.TS < last {
					t.Fatalf("video ts went back %d < %d", tg.TS, last)
				}
				last = tg.TS
				video++
			}
		}
		if video < 25 || last < 1800 {
			t.Errorf("tls %v: %d video tags, last ts %d", tlsOn, video, last)
		}
		s.Close()
	}
}

// The tap is a live FLV of a publish: ffmpeg re-publishes it to a second fake
// (an attached output, Epic 33 story 33.8 TC5) with the same timestamps, and the
// re-publisher ends when the source publisher disconnects.
func TestTapRepublishes(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	NeedFFmpeg(t)
	src, err := Listen(false)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := Listen(false)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	started := make(chan struct{}, 1)
	src.OnPublish = func(string) { started <- struct{}{} }
	pub := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "4", "-c:v", "libx264", "-preset", "ultrafast",
		"-g", "15", "-bf", "0", "-c:a", "aac", "-f", "flv", src.URL()+"in")
	if err := pub.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pub.Wait() }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("no publish")
	}
	time.Sleep(500 * time.Millisecond) // a late tap starts with the sequence headers
	tp := src.Tap("in")
	fwd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "flv", "-i", "pipe:0", "-c", "copy",
		"-f", "flv", dst.URL()+"out")
	fwd.Stdin = tp
	out, err := fwd.CombinedOutput()
	_ = tp.Close()
	if err != nil {
		t.Fatalf("re-publisher: %v\n%s", err, out)
	}
	ps := dst.Publishes()
	if len(ps) != 1 || ps[0].Key != "out" || ps[0].End.IsZero() {
		t.Fatalf("output publishes %+v", ps)
	}
	video, last := 0, uint32(0)
	for _, tg := range ps[0].Tags {
		if tg.Type == 9 && !tg.SeqHeader {
			if video > 0 && tg.TS < last {
				t.Fatalf("output video ts went back %d < %d", tg.TS, last)
			}
			last = tg.TS
			video++
		}
	}
	if video < 20 || last < 2500 {
		t.Fatalf("output: %d video tags, last ts %d", video, last)
	}
}
