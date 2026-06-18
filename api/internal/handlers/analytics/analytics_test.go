package analytics

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	now := time.Date(2026, 6, 17, 15, 30, 0, 0, time.UTC)

	if r := ParseRange("today", now); r.Label != "today" ||
		!r.Start.Equal(time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("today start=%v", r.Start)
	}
	if r := ParseRange("7d", now); !r.Start.Equal(now.AddDate(0, 0, -7)) {
		t.Errorf("7d start=%v", r.Start)
	}
	if r := ParseRange("30d", now); !r.Start.Equal(now.AddDate(0, 0, -30)) {
		t.Errorf("30d start=%v", r.Start)
	}
	if r := ParseRange("1y", now); !r.Start.Equal(now.AddDate(-1, 0, 0)) {
		t.Errorf("1y start=%v", r.Start)
	}
	// "all" has no lower bound.
	if r := ParseRange("all", now); r.HasLowerBound() {
		t.Error("all should have no lower bound")
	}
	// empty + garbage default to 7d.
	if r := ParseRange("", now); r.Label != "7d" {
		t.Errorf("empty default=%s want 7d", r.Label)
	}
	if r := ParseRange("bogus", now); r.Label != "7d" {
		t.Errorf("bogus default=%s want 7d", r.Label)
	}
}

func TestValidBucket(t *testing.T) {
	if validBucket("week") != "week" || validBucket("month") != "month" {
		t.Error("week/month should pass through")
	}
	if validBucket("") != "day" || validBucket("hour") != "day" {
		t.Error("unknown bucket should default to day")
	}
}

func TestBuildHeatmap(t *testing.T) {
	cells := []HeatCell{
		{Dow: 0, Hour: 0, WatchSec: 100},  // Sunday midnight
		{Dow: 6, Hour: 23, WatchSec: 50},  // Saturday 11pm
		{Dow: 3, Hour: 12, WatchSec: 10},  // Wed noon
		{Dow: 3, Hour: 12, WatchSec: 5},   // accumulates
		{Dow: 9, Hour: 99, WatchSec: 999}, // out of range, ignored
	}
	m := BuildHeatmap(cells)
	if m[0][0] != 100 {
		t.Errorf("sun midnight=%d want 100", m[0][0])
	}
	if m[6][23] != 50 {
		t.Errorf("sat 11pm=%d want 50", m[6][23])
	}
	if m[3][12] != 15 {
		t.Errorf("wed noon=%d want 15 (accumulated)", m[3][12])
	}
}

func TestExportRecordRoundTrip(t *testing.T) {
	// A field containing a comma, quote and newline must survive a CSV
	// round-trip (RFC 4180 via encoding/csv).
	row := exportRow{
		ID:              "s1",
		UserID:          "u1",
		VideoID:         "v1",
		StartedAt:       "2026-06-17T12:00:00Z",
		EndedAt:         "",
		DurationSec:     123,
		PercentComplete: 47.5,
		State:           "stopped",
		DeviceType:      `weird,"device"` + "\nline2",
		Platform:        "web",
		Quality:         "1080p",
	}
	rec := row.record()
	if len(rec) != len(exportHeader) {
		t.Fatalf("record len=%d header len=%d", len(rec), len(exportHeader))
	}
	if rec[5] != "123" {
		t.Errorf("duration field=%q want 123", rec[5])
	}
	if rec[6] != "47.50" {
		t.Errorf("percent field=%q want 47.50", rec[6])
	}

	var buf strings.Builder
	cw := csv.NewWriter(&buf)
	if err := cw.Write(exportHeader); err != nil {
		t.Fatal(err)
	}
	if err := cw.Write(rec); err != nil {
		t.Fatal(err)
	}
	cw.Flush()
	out := buf.String()
	// The nasty device value must be quoted, not split into extra columns.
	if !strings.Contains(out, `"weird,""device""`) {
		t.Errorf("device value not RFC-4180 escaped: %q", out)
	}
}

func TestClamp(t *testing.T) {
	if clamp(0, 10, 100) != 10 {
		t.Error("zero → default")
	}
	if clamp(500, 10, 100) != 100 {
		t.Error("over-max → max")
	}
	if clamp(42, 10, 100) != 42 {
		t.Error("in-range → as-is")
	}
}

func TestSummaryCache(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	c := newSummaryCache(30 * time.Second)
	if _, ok := c.get("7d", now); ok {
		t.Error("empty cache should miss")
	}
	c.put("7d", Summary{Range: "7d", TotalSessions: 5}, now)
	if v, ok := c.get("7d", now.Add(10*time.Second)); !ok || v.TotalSessions != 5 {
		t.Error("fresh entry should hit")
	}
	if _, ok := c.get("7d", now.Add(31*time.Second)); ok {
		t.Error("expired entry should miss")
	}
}
