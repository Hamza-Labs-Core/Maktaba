package channel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fillerRepo is a fixed schedule plus the channel's filler (FillerSource).
type fillerRepo struct {
	growingRepo
	fillers []ProgramBlock
	asked   int
}

func (r *fillerRepo) FillersAround(_ context.Context, _ uuid.UUID, at time.Time, limit int) ([]ProgramBlock, error) {
	r.asked++
	return nearestFirst(append([]ProgramBlock(nil), r.fillers...), at, limit), nil
}

// checked counts unreadableRun's checks.
var checked atomic.Int32

// unreadableRun runs the feeder (sequential mode) with Check refusing every
// path that holds "missing", and returns the segments and the reported blocks.
func unreadableRun(t *testing.T, repo ScheduleSource, clk *fakeClock, n int, fill string,
	fail func(Segment) bool, failErr error) ([]Segment, []int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakePlayer{clk: clk, stopAfter: n, cancel: cancel, fail: fail, failAfter: time.Minute,
		failErr: failErr, ctx: ctx}
	var mu sync.Mutex
	var reported []int64
	f := &Feeder{Source: repo, ChannelID: uuid.New(), Player: p, Now: clk.now,
		Sleep: func(_ context.Context, d time.Duration) { clk.advance(d) },
		WaitUntil: func(ctx context.Context, _ time.Time, done <-chan struct{}) {
			select {
			case <-done:
			case <-ctx.Done():
			}
		},
		Check: func(_ context.Context, b ProgramBlock) error {
			checked.Add(1)
			if strings.Contains(b.Path, "missing") {
				return fmt.Errorf("%w: %s: no such file", ErrSourceUnreadable, b.Path)
			}
			if strings.Contains(b.Path, "slow") {
				return fmt.Errorf("%w: %s: %w within 10s", ErrSourceUnreadable, b.Path, ErrSourceTimeout)
			}
			return nil
		},
		OnUnreadable: func(b ProgramBlock, _ error) {
			mu.Lock()
			reported = append(reported, b.Seq)
			mu.Unlock()
		},
		UnreadableFill: fill,
	}
	finished := make(chan struct{})
	go func() { f.Run(ctx); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("feeder did not stop")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	return append([]Segment(nil), p.played...), append([]int64(nil), reported...)
}

// dizqueTV parity: a 1-hour programme whose file is missing airs
// the channel's filler (nearest first, in turn) until its scheduled end, not an
// hour of slate; the next programme starts on time and the file is reported once.
func TestFeeder_UnreadableFileAirsFillerUntilBlockEnd(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	missing := blk(2, KindProgram, t0.Add(10*time.Minute), time.Hour)
	missing.Path = "/media/missing.mp4"
	repo := &fillerRepo{growingRepo: growingRepo{blocks: []ProgramBlock{
		blk(1, KindProgram, t0, 10*time.Minute),
		missing,
		blk(3, KindProgram, t0.Add(70*time.Minute), 10*time.Minute),
	}}}
	fa := blk(90, KindFiller, t0.Add(9*time.Minute), 20*time.Minute) // nearest
	fb := blk(91, KindFiller, t0.Add(90*time.Minute), 15*time.Minute)
	gone := blk(92, KindFiller, t0.Add(10*time.Minute), 5*time.Minute) // nearer still, unreadable
	gone.Path = "/media/missing-filler.mp4"
	repo.fillers = []ProgramBlock{fb, fa, gone}

	segs, reported := unreadableRun(t, repo, clk, 7, FillFiller, nil, nil)
	assertContiguous(t, segs)
	want := []struct {
		kind string
		seq  int64
		mins int
	}{
		{KindProgram, 1, 10}, {KindFiller, 90, 20}, {KindFiller, 91, 15}, {KindFiller, 90, 20},
		{KindFiller, 91, 5}, // cut at the missing programme's end
		{KindProgram, 3, 10},
	}
	for i, w := range want {
		s := segs[i]
		if s.Kind != w.kind || s.Block.Seq != w.seq || s.Duration() != time.Duration(w.mins)*time.Minute {
			t.Fatalf("segment %d = %s seq %d %v, want %s seq %d %d min", i, s.Kind, s.Block.Seq, s.Duration(),
				w.kind, w.seq, w.mins)
		}
		if w.seq >= 90 && (!s.Synthetic || s.Next == nil || s.Next.Seq != 3) {
			t.Fatalf("stand-in %d: synthetic %v next %+v", i, s.Synthetic, s.Next)
		}
	}
	if !segs[5].Start.Equal(missing.EndAt) {
		t.Fatalf("next programme starts %v, scheduled %v", segs[5].Start, missing.EndAt)
	}
	if len(reported) != 2 || reported[0] != 2 || reported[1] != 92 {
		t.Fatalf("reported %v, want the programme then the unreadable filler, once each", reported)
	}
}

// unreadable_file_fill = slate keeps the slate, re-planned every few minutes.
func TestFeeder_UnreadableFileSlateRule(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	missing := blk(1, KindProgram, t0, 12*time.Minute)
	missing.Path = "/media/missing.mp4"
	repo := &fillerRepo{growingRepo: growingRepo{blocks: []ProgramBlock{missing,
		blk(2, KindProgram, t0.Add(12*time.Minute), 10*time.Minute)}},
		fillers: []ProgramBlock{blk(90, KindFiller, t0, 5*time.Minute)}}
	segs, reported := unreadableRun(t, repo, clk, 4, FillSlate, nil, nil)
	assertContiguous(t, segs)
	for i, d := range []time.Duration{maxGapSlate, maxGapSlate, 2 * time.Minute} {
		if segs[i].Kind != KindSlate || !segs[i].Synthetic || segs[i].Duration() != d {
			t.Fatalf("segment %d = %+v, want a %v slate", i, segs[i], d)
		}
	}
	if segs[3].Block.Seq != 2 || repo.asked != 0 || len(reported) != 1 {
		t.Fatalf("after: %+v, fillers asked %d, reported %v", segs[3], repo.asked, reported)
	}
}

// A file that fails while it airs (the probe refuses it, the share stalls)
// gets the stand-in for the rest of its span; any other failure keeps the slate.
func TestFeeder_UnreadableAtRuntimeAirsFiller(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	prog := blk(1, KindProgram, t0, 30*time.Minute)
	repo := &fillerRepo{growingRepo: growingRepo{blocks: []ProgramBlock{prog,
		blk(2, KindProgram, t0.Add(30*time.Minute), 10*time.Minute)}},
		fillers: []ProgramBlock{blk(90, KindFiller, t0.Add(time.Hour), 40*time.Minute)}}
	fail := func(s Segment) bool { return s.Block.Seq == 1 }
	segs, reported := unreadableRun(t, repo, clk, 3, FillFiller, fail,
		fmt.Errorf("%w: no output for 30s", ErrSourceUnreadable))
	if s := segs[1]; s.Block.Seq != 90 || !s.Start.Equal(t0.Add(time.Minute)) || s.End != prog.EndAt || s.SeekMS != 0 {
		t.Fatalf("after the failure: %+v", segs[1])
	}
	if segs[2].Block.Seq != 2 || len(reported) != 1 || reported[0] != 1 {
		t.Fatalf("then %+v, reported %v", segs[2], reported)
	}

	clk = newClock(t0)
	segs, reported = unreadableRun(t, repo, clk, 2, FillFiller, fail, errors.New("ffmpeg crashed"))
	if segs[1].Kind != KindSlate || segs[1].Block.Seq != 1 || len(reported) != 0 {
		t.Fatalf("a non-file failure: %+v, reported %v", segs[1], reported)
	}
}

// With no filler at all the block falls back to the slate.
func TestFeeder_UnreadableWithoutFillerSlates(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	missing := blk(1, KindProgram, t0, 3*time.Minute)
	missing.Path = "/media/missing.mp4"
	repo := &fillerRepo{growingRepo: growingRepo{blocks: []ProgramBlock{missing,
		blk(2, KindProgram, t0.Add(3*time.Minute), 10*time.Minute)}}}
	segs, _ := unreadableRun(t, repo, clk, 2, FillFiller, nil, nil)
	if segs[0].Kind != KindSlate || segs[0].End != missing.EndAt || segs[1].Block.Seq != 2 {
		t.Fatalf("segments %+v", segs)
	}
}

// A filler whose share does not answer ends the search: the others on that
// share would each cost the source timeout too.
func TestFeeder_SlowShareStopsTheFillerSearch(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	missing := blk(1, KindProgram, t0, 3*time.Minute)
	missing.Path = "/media/missing.mp4"
	slow := blk(90, KindFiller, t0, time.Minute)
	slow.Path = "/media/slow-a.mp4"
	slow2 := blk(91, KindFiller, t0.Add(time.Hour), time.Minute)
	slow2.Path = "/media/slow-b.mp4"
	repo := &fillerRepo{growingRepo: growingRepo{blocks: []ProgramBlock{missing,
		blk(2, KindProgram, t0.Add(3*time.Minute), 10*time.Minute)}},
		fillers: []ProgramBlock{slow, slow2}}
	checked.Store(0)
	segs, reported := unreadableRun(t, repo, clk, 1, FillFiller, nil, nil)
	if segs[0].Kind != KindSlate || len(reported) != 2 || checked.Load() != 2 {
		t.Fatalf("segment %+v, reported %v, %d checks (the programme, one filler)", segs[0], reported, checked.Load())
	}
}

func TestStatWithin(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.mp4")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	ctx := context.Background()
	if err := statWithin(ctx, f, time.Second); err != nil {
		t.Fatalf("present file: %v", err)
	}
	for _, p := range []string{filepath.Join(dir, "gone.mp4"), dir} {
		if err := statWithin(ctx, p, time.Second); !errors.Is(err, ErrSourceUnreadable) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	// a share that does not answer: unreadable after the timeout, not stuck
	release := make(chan struct{})
	defer close(release)
	prev := statFile
	statFile = func(string) (os.FileInfo, error) { <-release; return nil, errors.New("late") }
	defer func() { statFile = prev }()
	start := time.Now()
	err := statWithin(ctx, f, 50*time.Millisecond)
	if !errors.Is(err, ErrSourceUnreadable) || !errors.Is(err, ErrSourceTimeout) || time.Since(start) > 2*time.Second {
		t.Fatalf("hung share: %v after %v", err, time.Since(start))
	}
}

func TestStallWatch(t *testing.T) {
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	stopped := make(chan struct{})
	go stallWatch(40*time.Millisecond, &last, make(chan struct{}), func() { close(stopped) })
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("a silent block was not stopped")
	}
	// output keeps flowing: never stopped; done ends the watch
	done := make(chan struct{})
	var fired atomic.Bool
	go stallWatch(40*time.Millisecond, &last, done, func() { fired.Store(true) })
	for i := 0; i < 10; i++ {
		last.Store(time.Now().UnixNano())
		time.Sleep(10 * time.Millisecond)
	}
	close(done)
	if fired.Load() {
		t.Fatal("a block with output was stopped")
	}
}

// reportRepo records the reported videos (UnreadableReporter).
type reportRepo struct {
	syncRepo
	mu  sync.Mutex
	got []uuid.UUID
}

func (r *reportRepo) ReportUnreadable(_ context.Context, id uuid.UUID, _ string) error {
	r.mu.Lock()
	r.got = append(r.got, id)
	r.mu.Unlock()
	return nil
}

func (r *reportRepo) reported() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID(nil), r.got...)
}

// A share that hangs mid-read: the block's ffmpeg is ended after the stall
// rule (even with the next block already queued) and the video is reported; a
// missing file is caught before it starts and slated (no filler here).
func TestStation_StalledAndMissingFiles(t *testing.T) {
	t.Setenv(fakeFFmpegEnv, "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	media := t.TempDir()
	stall := filepath.Join(media, "stall.mp4")
	_ = os.WriteFile(stall, []byte("x"), 0o644)
	now := time.Now()
	stallVid, goneVid := uuid.New(), uuid.New()
	repo := &reportRepo{syncRepo: syncRepo{blocks: []ProgramBlock{
		{Seq: 1, Kind: KindProgram, Path: stall, VideoID: stallVid, StartAt: now.Add(-time.Second),
			EndAt: now.Add(time.Second), SourceDurationMS: 2000},
		{Seq: 2, Kind: KindProgram, Path: filepath.Join(media, "gone.mp4"), VideoID: goneVid,
			StartAt: now.Add(time.Second), EndAt: now.Add(time.Minute), SourceDurationMS: 59000},
	}}}
	st, err := StartStation(StationConfig{
		FFmpeg: bin, Profile: OutputProfile{Width: 320, Height: 180, FPS: 30, Encoder: EncoderSoftware,
			SegmentSeconds: 1, HLSListSize: 1},
		Repo: repo, Prober: stubProber{}, Resolver: NewYtDlp(""), Logger: testLogger(t),
		SourceStall: 150 * time.Millisecond, SourceTimeout: time.Second, ReportUnreadable: true,
	}, uuidFor(t), filepath.Join(t.TempDir(), "hls"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		st.Stop()
		<-st.Done()
	}()
	log := func() string {
		b, _ := os.ReadFile(filepath.Join(st.Dir(), "blocks.log"))
		return string(b)
	}
	if !waitForSoft(5*time.Second, func() bool {
		l := log()
		return strings.Contains(l, "  stalled ") && strings.Contains(l, " slate src=") && len(repo.reported()) == 2
	}) {
		t.Fatalf("reported %v; blocks.log:\n%s", repo.reported(), log())
	}
	if r := repo.reported(); r[0] != goneVid || r[1] != stallVid {
		t.Fatalf("reported %v, want the missing (checked when queued) then the stalled video", r)
	}
	if strings.Contains(log(), "gone.mp4") {
		t.Fatalf("the missing file was started:\n%s", log())
	}
}

func TestApplyOutputRules_UnreadableFile(t *testing.T) {
	base := OutputSettingsFromEnv()
	if base.UnreadableFill != FillFiller || base.SourceTimeout != DefaultSourceTimeout ||
		base.SourceStall != DefaultSourceStall || !base.ReportUnreadable {
		t.Fatalf("defaults %+v", base)
	}
	out, bad := ApplyOutputRules(base, []byte(`{"unreadable_file_fill":"slate","source_timeout_seconds":20,
		"source_stall_seconds":0,"report_unreadable_files":false}`))
	if len(bad) != 0 || out.UnreadableFill != FillSlate || out.SourceTimeout != 20*time.Second ||
		out.SourceStall != 0 || out.ReportUnreadable {
		t.Fatalf("applied %+v bad %v", out, bad)
	}
	out, bad = ApplyOutputRules(base, []byte(`{"unreadable_file_fill":"black","source_timeout_seconds":0,
		"source_stall_seconds":601,"report_unreadable_files":"yes"}`))
	if len(bad) != 4 || out.UnreadableFill != base.UnreadableFill || out.SourceTimeout != base.SourceTimeout ||
		out.SourceStall != base.SourceStall || !out.ReportUnreadable {
		t.Fatalf("refused values must keep the base: %+v bad %v", out, bad)
	}
}
