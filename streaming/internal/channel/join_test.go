package channel

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func mkBlock(start, end string, offsetMS, durMS int) ProgramBlock {
	return ProgramBlock{
		VideoID:          uuid.New(),
		StartAt:          tm(start),
		EndAt:            tm(end),
		SourceOffsetMS:   offsetMS,
		SourceDurationMS: durMS,
		Path:             "/media/x.mkv",
	}
}

func tm(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestLocate_CurrentBlockSeek(t *testing.T) {
	blocks := []ProgramBlock{
		mkBlock("2026-06-14T20:00:00Z", "2026-06-14T21:00:00Z", 0, 3600_000),
	}
	// 20 minutes in → seek should be 20 min = 1_200_000 ms.
	j, err := Locate(blocks, tm("2026-06-14T20:20:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if j.Index != 0 || j.SeekMS != 1_200_000 {
		t.Errorf("got index=%d seek=%d", j.Index, j.SeekMS)
	}
}

func TestLocate_SeekAddsSourceOffset(t *testing.T) {
	// A marathon block that starts 10 min into the source file.
	blocks := []ProgramBlock{
		mkBlock("2026-06-14T20:00:00Z", "2026-06-14T20:30:00Z", 600_000, 1_800_000),
	}
	j, err := Locate(blocks, tm("2026-06-14T20:05:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	// 5 min into the block + 10 min source offset = 15 min = 900_000 ms.
	if j.SeekMS != 900_000 {
		t.Errorf("seek = %d, want 900000", j.SeekMS)
	}
}

func TestLocate_BeforeWindowJoinsFirst(t *testing.T) {
	blocks := []ProgramBlock{
		mkBlock("2026-06-14T20:00:00Z", "2026-06-14T21:00:00Z", 5000, 3600_000),
	}
	j, err := Locate(blocks, tm("2026-06-14T19:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if j.Index != 0 || j.SeekMS != 5000 {
		t.Errorf("before-window join wrong: index=%d seek=%d", j.Index, j.SeekMS)
	}
}

func TestLocate_BoundaryAdvancesToNext(t *testing.T) {
	blocks := []ProgramBlock{
		mkBlock("2026-06-14T20:00:00Z", "2026-06-14T20:30:00Z", 0, 1_800_000),
		mkBlock("2026-06-14T20:30:00Z", "2026-06-14T21:00:00Z", 0, 1_800_000),
	}
	j, err := Locate(blocks, tm("2026-06-14T20:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if j.Index != 1 || j.SeekMS != 0 {
		t.Errorf("boundary should advance to next at offset 0: index=%d seek=%d", j.Index, j.SeekMS)
	}
}

func TestLocate_AfterEndIsNoProgram(t *testing.T) {
	blocks := []ProgramBlock{
		mkBlock("2026-06-14T20:00:00Z", "2026-06-14T21:00:00Z", 0, 3600_000),
	}
	if _, err := Locate(blocks, tm("2026-06-14T22:00:00Z")); err != ErrNoProgram {
		t.Errorf("expected ErrNoProgram, got %v", err)
	}
}

func TestLocate_EmptyIsNoProgram(t *testing.T) {
	if _, err := Locate(nil, tm("2026-06-14T20:00:00Z")); err != ErrNoProgram {
		t.Errorf("expected ErrNoProgram, got %v", err)
	}
}
