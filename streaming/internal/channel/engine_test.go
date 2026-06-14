package channel

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestEngine(t *testing.T, blocks []ProgramBlock, clk *fakeClock) (*Engine, *fakeRepo, *fakeRunner) {
	t.Helper()
	repo := &fakeRepo{blocks: blocks}
	runner := &fakeRunner{}
	eng := NewEngine(repo, runner, fakeLayout{root: t.TempDir()}, 4, clk.now)
	return eng, repo, runner
}

func liveBlocks(clk *fakeClock) []ProgramBlock {
	now := clk.now()
	return []ProgramBlock{
		{VideoID: uuid.New(), StartAt: now.Add(-10 * time.Minute), EndAt: now.Add(20 * time.Minute),
			SourceOffsetMS: 0, SourceDurationMS: 1_800_000, Path: "/media/a.mkv"},
		{VideoID: uuid.New(), StartAt: now.Add(20 * time.Minute), EndAt: now.Add(50 * time.Minute),
			SourceOffsetMS: 0, SourceDurationMS: 1_800_000, Path: "/media/b.mkv"},
	}
}

func TestEngine_ColdTuneSpawns(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	eng, repo, runner := newTestEngine(t, liveBlocks(clk), clk)

	id := uuid.New()
	res, err := eng.Tune(context.Background(), id)
	if err != nil {
		t.Fatalf("tune: %v", err)
	}
	if runner.starts != 1 {
		t.Errorf("cold tune should spawn once, got %d", runner.starts)
	}
	if res.ManifestURL == "" {
		t.Error("manifest url should be set")
	}
	if repo.setCalls == 0 {
		t.Error("runtime should be written on tune")
	}
	if eng.ActiveCount() != 1 {
		t.Errorf("active count = %d, want 1", eng.ActiveCount())
	}
	// The concat script must have been written and reference the head file.
	if !bytes.Contains([]byte(runner.lastJob.ConcatPath), []byte("concat.ffconcat")) {
		t.Errorf("concat path unexpected: %q", runner.lastJob.ConcatPath)
	}
}

func TestEngine_WarmReTuneDoesNotRespawn(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	eng, _, runner := newTestEngine(t, liveBlocks(clk), clk)
	id := uuid.New()

	if _, err := eng.Tune(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	eng.Detach(id) // viewer leaves → warm
	if _, err := eng.Tune(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if runner.starts != 1 {
		t.Errorf("warm re-tune must NOT respawn; starts = %d", runner.starts)
	}
}

func TestEngine_NoProgramErrors(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	// All blocks already ended.
	now := clk.now()
	past := []ProgramBlock{
		{VideoID: uuid.New(), StartAt: now.Add(-2 * time.Hour), EndAt: now.Add(-1 * time.Hour),
			SourceDurationMS: 3_600_000, Path: "/media/a.mkv"},
	}
	eng, _, runner := newTestEngine(t, past, clk)
	if _, err := eng.Tune(context.Background(), uuid.New()); err != ErrNoProgram {
		t.Errorf("expected ErrNoProgram, got %v", err)
	}
	if runner.starts != 0 {
		t.Error("no encoder should spawn when nothing is on air")
	}
}

func TestEngine_ReapIdleClearsRuntime(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	eng, repo, _ := newTestEngine(t, liveBlocks(clk), clk)
	eng.WarmGrace = 60 * time.Second
	id := uuid.New()

	if _, err := eng.Tune(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	eng.Detach(id)
	clk.advance(2 * time.Minute)
	reaped := eng.ReapIdle(context.Background())
	if len(reaped) != 1 || reaped[0] != id {
		t.Fatalf("expected reap of %v, got %v", id, reaped)
	}
	// The stop closure clears the runtime row.
	if len(repo.cleared) != 1 || repo.cleared[0] != id {
		t.Errorf("runtime should be cleared on reap, got %v", repo.cleared)
	}
	if eng.ActiveCount() != 0 {
		t.Errorf("active count after reap = %d, want 0", eng.ActiveCount())
	}
}

func TestEngine_ServeMPEGTS(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	eng, _, _ := newTestEngine(t, liveBlocks(clk), clk)
	ts := &fakeTS{}
	var buf bytes.Buffer
	if err := eng.ServeMPEGTS(context.Background(), uuid.New(), ts, &buf); err != nil {
		t.Fatalf("ServeMPEGTS: %v", err)
	}
	if ts.calls != 1 {
		t.Errorf("ts runner should be invoked once, got %d", ts.calls)
	}
	if buf.String() != "TSPACKET" {
		t.Errorf("ts output not written through: %q", buf.String())
	}
}
