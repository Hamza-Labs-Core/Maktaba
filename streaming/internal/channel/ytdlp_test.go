package channel

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const ytPage = "https://www.youtube.com/@aljazeera/live"

// TestYtDlp_ArgsMatchSearch: the channel's yt-dlp ignores the host's yt-dlp
// config and never updates itself (as the search's does), and asks for the
// channel's output height.
func TestYtDlp_ArgsMatchSearch(t *testing.T) {
	y := NewYtDlp("yt")
	var got []string
	y.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		got = args
		return []byte("https://cdn/x.m3u8\n"), nil
	}
	if _, err := y.Resolve(context.Background(), LiveRequest{SourceURL: ytPage, MaxHeight: 720}); err != nil {
		t.Fatal(err)
	}
	if got[0] != "-f" || got[1] != "best[height<=720][protocol^=m3u8]/best" {
		t.Errorf("format: %v", got)
	}
	for _, f := range []string{"--ignore-config", "--no-update", "--no-playlist", "-g"} {
		if i := slices.Index(got, f); i < 0 || i > slices.Index(got, "--") {
			t.Errorf("%s missing before \"--\": %v", f, got)
		}
	}
	if got[len(got)-1] != ytPage {
		t.Errorf("source last: %v", got)
	}
}

// TestYtDlp_HeightKeysTheLookup: channels at different output heights each get
// their own rendition; no height asks for the default profile's.
func TestYtDlp_HeightKeysTheLookup(t *testing.T) {
	y := NewYtDlp("yt")
	var formats []string
	y.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		formats = append(formats, args[1])
		return []byte("https://cdn/" + args[1] + ".m3u8\n"), nil
	}
	ctx := context.Background()
	a, _ := y.Resolve(ctx, LiveRequest{SourceURL: ytPage, MaxHeight: 720})
	b, _ := y.Resolve(ctx, LiveRequest{SourceURL: ytPage})
	c, _ := y.Resolve(ctx, LiveRequest{SourceURL: ytPage, MaxHeight: DefaultProfile().Height})
	if len(formats) != 2 || a == b || b != c {
		t.Errorf("formats %v; %q %q %q", formats, a, b, c)
	}
	if formats[1] != "best[height<=1080][protocol^=m3u8]/best" {
		t.Errorf("default height: %v", formats)
	}
	y.Forget(ytPage)
	_, _ = y.Resolve(ctx, LiveRequest{SourceURL: ytPage, MaxHeight: 720})
	_, _ = y.Resolve(ctx, LiveRequest{SourceURL: ytPage})
	if len(formats) != 4 {
		t.Errorf("Forget must drop every height: %v", formats)
	}
}

// TestYtDlp_ReuseFollowsRequest: a lookup is reused only while it is younger
// than the request's Reuse. The clock is the test's: Windows' coarse clock can
// read the same before and after a lookup, which is then 0 s old.
func TestYtDlp_ReuseFollowsRequest(t *testing.T) {
	y := NewYtDlp("yt")
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	y.now = func() time.Time { return now }
	var calls atomic.Int32
	y.run = func(context.Context, string, ...string) ([]byte, error) {
		calls.Add(1)
		return []byte("https://cdn/x.m3u8\n"), nil
	}
	ctx := context.Background()
	_, _ = y.Resolve(ctx, LiveRequest{SourceURL: ytPage, Reuse: time.Hour})
	now = now.Add(30 * time.Minute)
	_, _ = y.Resolve(ctx, LiveRequest{SourceURL: ytPage, Reuse: time.Hour})
	if calls.Load() != 1 {
		t.Fatalf("within reuse: %d lookups", calls.Load())
	}
	_, _ = y.Resolve(ctx, LiveRequest{SourceURL: ytPage, Reuse: 10 * time.Minute})
	if calls.Load() != 2 {
		t.Errorf("past reuse: %d lookups", calls.Load())
	}
}

// TestYtDlp_ConcurrentChannelsShareOneLookup: channels starting the same live
// page at once wait on one yt-dlp run instead of each starting one.
func TestYtDlp_ConcurrentChannelsShareOneLookup(t *testing.T) {
	y := NewYtDlp("yt")
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	y.run = func(context.Context, string, ...string) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return []byte("https://cdn/shared.m3u8\n"), nil
	}
	const n = 8
	var wg sync.WaitGroup
	got := make([]string, n)
	errs := make([]error, n)
	resolve := func(i int) {
		defer wg.Done()
		got[i], errs[i] = y.Resolve(context.Background(), LiveRequest{SourceURL: ytPage, MaxHeight: 720})
	}
	wg.Add(1)
	go resolve(0)
	<-entered // the first lookup is running and nothing is cached yet
	var ready sync.WaitGroup
	for i := 1; i < n; i++ {
		wg.Add(1)
		ready.Add(1)
		go func() { ready.Done(); resolve(i) }()
	}
	ready.Wait()
	time.Sleep(50 * time.Millisecond) // let them reach the running lookup
	close(release)
	wg.Wait()
	if c := calls.Load(); c != 1 {
		t.Errorf("yt-dlp ran %d times, want 1", c)
	}
	for i := range got {
		if errs[i] != nil || got[i] != "https://cdn/shared.m3u8" {
			t.Errorf("caller %d: %q %v", i, got[i], errs[i])
		}
	}
}

// TestYtDlp_CallerCancelDoesNotFailOthers: the station that started a shared
// lookup stopping leaves the lookup running for the others.
func TestYtDlp_CallerCancelDoesNotFailOthers(t *testing.T) {
	y := NewYtDlp("yt")
	entered, release := make(chan struct{}), make(chan struct{})
	var runs atomic.Int32
	var runErr error
	y.run = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		if runs.Add(1) == 1 { // a second run is counted, below
			close(entered)
			<-release
			runErr = ctx.Err()
		}
		return []byte("https://cdn/x.m3u8\n"), nil
	}
	first, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := y.Resolve(first, LiveRequest{SourceURL: ytPage})
		firstErr <- err
	}()
	<-entered
	second := make(chan string, 1)
	go func() {
		u, _ := y.Resolve(context.Background(), LiveRequest{SourceURL: ytPage})
		second <- u
	}()
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled caller: %v", err)
	}
	close(release)
	if u := <-second; u != "https://cdn/x.m3u8" {
		t.Errorf("other caller: %q", u)
	}
	if runErr != nil {
		t.Errorf("the shared lookup was cancelled: %v", runErr)
	}
	if n := runs.Load(); n != 1 {
		t.Errorf("yt-dlp ran %d times, want 1", n)
	}
}

// TestYtDlp_CallerMissingTheEndOfARunDoesNotRunAgain: a caller that found
// nothing cached while the shared run was going, and reaches DoChan only after
// the run has ended and left the group, takes the URL the run cached: yt-dlp
// does not run a second time. (CI caught this as the fake run above closing
// its channel twice.)
func TestYtDlp_CallerMissingTheEndOfARunDoesNotRunAgain(t *testing.T) {
	y := NewYtDlp("yt")
	var runs atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	y.run = func(context.Context, string, ...string) ([]byte, error) {
		if runs.Add(1) == 1 {
			close(entered)
			<-release
		}
		return []byte("https://cdn/x.m3u8\n"), nil
	}
	var misses atomic.Int32
	held, resume := make(chan struct{}), make(chan struct{})
	y.beforeJoin = func() {
		if misses.Add(1) == 2 { // the second caller, past its cache miss
			close(held)
			<-resume
		}
	}
	req := LiveRequest{SourceURL: ytPage}
	first := make(chan string, 1)
	go func() {
		u, _ := y.Resolve(context.Background(), req)
		first <- u
	}()
	<-entered
	second := make(chan string, 1)
	go func() {
		u, _ := y.Resolve(context.Background(), req)
		second <- u
	}()
	<-held         // nothing cached yet: the run is still going
	close(release) // the run caches its URL and ends
	// singleflight takes the run out of the group before it answers its callers
	if u := <-first; u != "https://cdn/x.m3u8" {
		t.Errorf("first caller: %q", u)
	}
	close(resume)
	if u := <-second; u != "https://cdn/x.m3u8" {
		t.Errorf("second caller: %q", u)
	}
	if n := runs.Load(); n != 1 {
		t.Errorf("yt-dlp ran %d times, want 1", n)
	}
}

// TestYtDlp_FailedLookupNotReused: a failed lookup is not cached; the next
// block looks again.
func TestYtDlp_FailedLookupNotReused(t *testing.T) {
	y := NewYtDlp("yt")
	var calls atomic.Int32
	y.run = func(context.Context, string, ...string) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("offline")
		}
		return []byte("https://cdn/x.m3u8\n"), nil
	}
	ctx := context.Background()
	if _, err := y.Resolve(ctx, LiveRequest{SourceURL: ytPage}); err == nil {
		t.Fatal("failure must surface")
	}
	if u, err := y.Resolve(ctx, LiveRequest{SourceURL: ytPage}); err != nil || u != "https://cdn/x.m3u8" {
		t.Errorf("retry: %q %v", u, err)
	}
}
