package hdhr

import (
	"context"
	"encoding/xml"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testRules are the rules' defaults (tv.guide_*: 120 s, «فواصل», 60 s).
var testRules = GuideRulesFromSeconds(120, "فواصل", 60)

// The guide's filler rules, as dizqueTV's: filler up to 120 s joins the
// programme before it, longer filler is a «فواصل» entry, and programmes
// shorter than 60 s are not listed.
func TestCollapse_GuideRules(t *testing.T) {
	t0 := time.Date(2026, 6, 14, 20, 0, 0, 0, time.UTC)
	ch := uuid.New()
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	prog := func(title string, from, to int) GuideProgram {
		return GuideProgram{ChannelID: ch, Kind: "program", StartAt: at(from), EndAt: at(to),
			Snapshot: snap(map[string]any{"title": title})}
	}
	fill := func(kind string, from, to int) GuideProgram {
		return GuideProgram{ChannelID: ch, Kind: kind, StartAt: at(from), EndAt: at(to)}
	}
	progs := []GuideProgram{
		prog("A", 0, 1800),
		fill("bumper", 1800, 1830), fill("filler", 1830, 1920), // 120 s: folded into A
		prog("B", 1920, 3600),
		fill("filler", 3600, 3700), fill("slate", 3700, 3721), // 121 s: its own entry
		prog("C", 3721, 5400),
		prog("Tiny", 5400, 5459), // 59 s: not listed, folded into C
		prog("D", 5459, 7200),
		prog("Minute", 7200, 7260), // 60 s: listed
	}
	type row struct {
		title       string
		start, stop int
	}
	want := []row{{"A", 0, 1920}, {"B", 1920, 3600}, {"فواصل", 3600, 3721}, {"C", 3721, 5459},
		{"D", 5459, 7200}, {"Minute", 7200, 7260}}
	got := collapse("Kids", progs, testRules)
	if len(got) != len(want) {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	for i, w := range want {
		e := got[i]
		if e.snap.Title != w.title || !e.start.Equal(at(w.start)) || !e.stop.Equal(at(w.stop)) {
			t.Errorf("entry %d = %q %s-%s; want %q %d-%d s", i, e.snap.Title,
				e.start.Format(time.TimeOnly), e.stop.Format(time.TimeOnly), w.title, w.start, w.stop)
		}
	}
	if !got[2].isFiller {
		t.Error("the long filler entry is not marked filler")
	}

	// A short lead-in joins the programme after it; a long one is its own
	// entry; an empty filler title is the channel's name.
	lead := []GuideProgram{fill("filler", 0, 120), prog("A", 120, 1800)}
	if got := collapse("Kids", lead, testRules); len(got) != 1 || got[0].snap.Title != "A" || !got[0].start.Equal(at(0)) {
		t.Errorf("short lead-in: %+v", got)
	}
	lead = []GuideProgram{fill("slate", 0, 600), prog("A", 600, 1800)}
	got = collapse("Kids", lead, GuideRulesFromSeconds(120, "", 60))
	if len(got) != 2 || got[0].snap.Title != "Kids" || got[1].snap.Title != "A" {
		t.Errorf("long lead-in: %+v", got)
	}
}

func TestGuideHandler_FollowsRulesFunc(t *testing.T) {
	g, _, t0 := guideFixture()
	h := &GuideHandler{Repo: g, Now: func() time.Time { return t0.Add(time.Hour) },
		RulesFunc: func(context.Context) GuideRules { return GuideRulesFromSeconds(0, "Breaks", 60) }}
	rec := httptest.NewRecorder()
	h.XMLTV(rec, httptest.NewRequest("GET", "/xmltv.xml", nil))
	var doc xmltvDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	// max padding 0: every filler run (the 2-minute lead-in, the bumper
	// pair, the slate) is its own «Breaks» entry
	n := 0
	for _, p := range doc.Programmes {
		if p.Title == "Breaks" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("got %d filler entries, want 3: %+v", n, doc.Programmes)
	}
}
