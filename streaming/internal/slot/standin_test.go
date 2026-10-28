package slot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// A filler airing in place of a programme whose file this host cannot read is
// reported with the programme it stands in for: the staging verification tells it
// from a wrong block (TV change #53, 2026-10-28).
func TestStationInfoReportsTheStandIn(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 4, 41, 43, 0, time.UTC)
	filler := channel.ProgramBlock{Seq: 1477, Kind: channel.KindFiller, VideoID: uuid.New(),
		StartAt: t0.Add(-time.Hour), EndAt: t0.Add(-50 * time.Minute), Info: channel.BlockInfo{Title: "filler"}}
	seg := &channel.Segment{Kind: channel.KindFiller, Block: filler, Start: t0, End: t0.Add(36 * time.Second),
		Synthetic: true, StandInFor: 1474}
	b := stationInfo(uuid.New(), channel.StationStats{OnAir: seg}).Block
	if b == nil || b.StandInFor != 1474 || !b.Synthetic || b.Seq != 1477 || !b.StartAt.Equal(t0) {
		t.Fatalf("block %+v", b)
	}
	raw, _ := json.Marshal(b)
	if !strings.Contains(string(raw), `"stand_in_for":1474`) {
		t.Fatalf("json %s", raw)
	}
	seg.StandInFor = 0
	raw, _ = json.Marshal(stationInfo(uuid.New(), channel.StationStats{OnAir: seg}).Block)
	if strings.Contains(string(raw), "stand_in_for") {
		t.Fatalf("no stand-in: %s", raw)
	}
}
