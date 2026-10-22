package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dynamicMPD = `<?xml version="1.0" encoding="utf-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"
	profiles="urn:mpeg:dash:profile:isoff-live:2011"
	type="dynamic"
	minimumUpdatePeriod="PT10S"
	suggestedPresentationDelay="PT10S"
	availabilityStartTime="2026-10-22T13:14:42.366Z"
	publishTime="2026-10-22T13:15:11.274Z"
	maxSegmentDuration="PT6.0S"
	minBufferTime="PT20.0S">
	<Period id="0" start="PT0.0S"></Period>
</MPD>
`

const staticMPD = `<?xml version="1.0" encoding="utf-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"
	profiles="urn:mpeg:dash:profile:isoff-live:2011"
	type="static"
	mediaPresentationDuration="PT3M0.0S"
	maxSegmentDuration="PT6.0S"
	minBufferTime="PT20.0S">
	<Period id="0" start="PT0.0S"></Period>
</MPD>
`

func TestParseISODuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"PT3M0.0S": 3 * time.Minute, "PT1H2M3.5S": time.Hour + 2*time.Minute + 3500*time.Millisecond,
		"PT40.0S": 40 * time.Second, "PT2H": 2 * time.Hour, "PT6S": 6 * time.Second,
	} {
		if got, ok := parseISODuration(in); !ok || got != want {
			t.Errorf("%s: %v %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "PT", "P1D", "3M", "PTxS"} {
		if _, ok := parseISODuration(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func mpdAttrs(t *testing.T, mpd []byte) string {
	t.Helper()
	tag := mpdTag.Find(mpd)
	if tag == nil {
		t.Fatalf("no MPD element in\n%s", mpd)
	}
	return string(tag)
}

// A transcode's MPD: dynamic while ffmpeg runs, a player starting at its first
// segment (the delay reaches past the start) and its start kept; once ffmpeg is
// done, when a player may have had it dynamic, dynamic with the whole timeline
// until every segment is available and the timeline has been out for
// finalUpdates, then static; static from the start when none had it.
func TestTranscodeMPD(t *testing.T) {
	dir := t.TempDir()
	ast, _ := time.Parse(time.RFC3339Nano, "2026-10-22T13:14:42.366Z")
	now := ast.Add(29 * time.Second)

	// static, nobody had it dynamic: as written
	if got := transcodeMPD([]byte(staticMPD), dir, now, now); string(got) != staticMPD {
		t.Errorf("unseen static:\n%s", got)
	}

	// running: the delay reaches back past the start, the start kept
	got := mpdAttrs(t, transcodeMPD([]byte(dynamicMPD), dir, now, now))
	for _, want := range []string{`type="dynamic"`, `suggestedPresentationDelay="PT89S"`, `minimumUpdatePeriod="PT10S"`,
		`availabilityStartTime="2026-10-22T13:14:42.366Z"`} {
		if !strings.Contains(got, want) {
			t.Errorf("running: no %s in %s", want, got)
		}
	}
	if strings.Count(got, "suggestedPresentationDelay") != 1 {
		t.Errorf("running: %s", got)
	}
	if kept, err := os.ReadFile(filepath.Join(dir, availabilityFile)); err != nil || string(kept) != "2026-10-22T13:14:42.366Z" {
		t.Fatalf("kept start %q %v", kept, err)
	}

	// done at 98 s, ahead of the clock: dynamic, the kept start, the whole
	// timeline, until its last segment (3 min) is available, and finalUpdates
	done := ast.Add(98 * time.Second)
	last := ast.Add(3*time.Minute + finalUpdates)
	now = last.Add(-time.Second)
	got = mpdAttrs(t, transcodeMPD([]byte(staticMPD), dir, done, now))
	for _, want := range []string{`type="dynamic"`, `availabilityStartTime="2026-10-22T13:14:42.366Z"`,
		`mediaPresentationDuration="PT3M0.0S"`, `minimumUpdatePeriod="PT6.0S"`, `suggestedPresentationDelay="PT254S"`,
		`publishTime="` + now.UTC().Format(mpdTimeFormat) + `"`} {
		if !strings.Contains(got, want) {
			t.Errorf("final updates: no %s in %s", want, got)
		}
	}
	if strings.Contains(got, `type="static"`) {
		t.Errorf("final updates: %s", got)
	}
	if got := transcodeMPD([]byte(staticMPD), dir, done, last); string(got) != staticMPD {
		t.Errorf("every segment available:\n%s", got)
	}
	// done behind the clock (at 4 min): the whole timeline is out for
	// finalUpdates first
	slow := ast.Add(4 * time.Minute)
	if got := mpdAttrs(t, transcodeMPD([]byte(staticMPD), dir, slow, slow.Add(finalUpdates-time.Second))); !strings.Contains(got, `type="dynamic"`) {
		t.Errorf("a slow transcode's final updates: %s", got)
	}
	if got := transcodeMPD([]byte(staticMPD), dir, slow, slow.Add(finalUpdates)); string(got) != staticMPD {
		t.Errorf("a slow transcode, after the final updates:\n%s", got)
	}
	// a static MPD without a duration it reads: static
	if got := transcodeMPD([]byte(strings.Replace(staticMPD, "PT3M0.0S", "P1D", 1)), dir, done, now); strings.Contains(string(got), `type="dynamic"`) {
		t.Errorf("no duration: %s", got)
	}
	// not an MPD it knows: as written
	for _, other := range []string{"<MPD/>", "not xml", `<MPD type="dynamic">`} {
		if got := transcodeMPD([]byte(other), t.TempDir(), now, now); string(got) != other {
			t.Errorf("%q: %q", other, got)
		}
	}
}
