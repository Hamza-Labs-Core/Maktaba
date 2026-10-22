package ffmpeg

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// WatchInterval is how often the server checks its ffmpeg again: one installed
// or replaced while it runs shows without a restart.
const WatchInterval = 10 * time.Minute

// Watch checks the tools find returns now, then every interval until ctx ends,
// and logs the verdict when it changes. The func it returns is the last report
// (nil before the first check ends): the supervisor's status serves it.
func Watch(ctx context.Context, find func() Tools, interval time.Duration, log io.Writer) func() *Report {
	var (
		mu   sync.Mutex
		last *Report
	)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			r := Check(ctx, find())
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			changed := last == nil || last.Summary() != r.Summary()
			last = &r
			mu.Unlock()
			if changed {
				fmt.Fprintln(log, "[ffmpeg] "+r.Summary())
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return func() *Report {
		mu.Lock()
		defer mu.Unlock()
		if last == nil {
			return nil
		}
		r := *last
		return &r
	}
}

// Summary is the report in a line (the log's, the first of `maktaba-server ffmpeg`).
func (r Report) Summary() string {
	if r.OK {
		return fmt.Sprintf("%s (FFmpeg %s) does what Maktaba needs; ffprobe %s", r.FFmpeg, r.Version, r.FFprobe)
	}
	s := r.Problem.Message
	if r.Install != nil {
		s += " " + r.Install.Message
	}
	return s
}
