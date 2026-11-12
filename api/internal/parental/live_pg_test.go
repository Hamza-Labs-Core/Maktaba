package parental_test

// Story 57.03, test case T07: one live connection's filter against a real
// migrated Postgres (MAKTABA_TEST_PG_URL): what it sends, that a change of the
// policy holds from the next event on, and what it reuses while the policy cache
// does not listen.

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

func TestLiveGuardPostgres(t *testing.T) {
	t.Parallel()
	e := setup(t)
	seedFilter(e)
	gate := parental.NewGate()
	cache := parental.NewCache(e.store) // no listener: every policy read goes to the database
	gate.SetCache(cache)
	ctx := context.Background()
	kid, teen, unrated := fID(1), fID(4), fID(6)
	child := &principal.Principal{UserID: uChild, Libraries: []string{libOpen}}
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1)`, uChild)

	type ev struct {
		typ     string
		payload map[string]any
	}
	cases := []struct {
		name, channel string
		ev            ev
		send, end     bool
	}{
		{"an allowed video", "home", ev{"video.added", map[string]any{"video_id": kid}}, true, false},
		{"a video above the limit", "home", ev{"video.added", map[string]any{"video_id": teen}}, false, false},
		{"an unrated video", "home", ev{"video.added", map[string]any{"video_id": unrated}}, false, false},
		{"one hidden among several", "home", ev{"videos.changed", map[string]any{"video_ids": []any{kid, teen}}}, false, false},
		{"a video that is gone", "home", ev{"video.removed", map[string]any{"video_id": "00000000-0000-4000-8000-00000000dead"}}, false, false},
		{"a series with an allowed episode", "home", ev{"series.changed", map[string]any{"series_id": fSeries}}, true, false},
		{"a collection with only hidden items", "home", ev{"collection.changed", map[string]any{"collection_id": fCol}}, false, false},
		{"an event that names nothing", "home", ev{"something.new", map[string]any{"n": 1}}, false, false},
		{"an event known to carry no content", "home", ev{"capabilities_changed", map[string]any{"etag": "x"}}, true, false},
		{"an id that is none", "home", ev{"video.added", map[string]any{"video_id": "x"}}, false, false},
		{"the library's own feed", "library:" + libOpen, ev{"video.added", map[string]any{"video_id": kid}}, true, false},
		{"the library's feed, a hidden video", "library:" + libOpen, ev{"video.added", map[string]any{"video_id": teen}}, false, false},
		{"an allowed video's own feed, nothing named", "playback:" + kid, ev{"position", map[string]any{"position_sec": 5}}, true, false},
		{"a hidden video's own feed", "playback:" + teen, ev{"position", map[string]any{"position_sec": 5}}, false, true},
		{"the operators' feed", "jobs", ev{"job.started", map[string]any{"job_id": 1}}, false, true},
		{"the queues' feed", "queues", ev{"queues.snapshot", nil}, false, true},
	}
	for _, c := range cases {
		send, end := gate.LiveGuard(child, c.channel).Event(ctx, c.ev.typ, c.ev.payload)
		if send != c.send || end != c.end {
			t.Errorf("child, %s: send=%v end=%v, want send=%v end=%v", c.name, send, end, c.send, c.end)
		}
		// every other account is sent everything
		for _, p := range []*principal.Principal{
			{UserID: uViewer, Libraries: []string{libOpen}}, {UserID: uGuardian, Libraries: []string{libOpen}},
			{UserID: uOtherG}, {UserID: uAdmin, IsAdmin: true, AccessAllLibraries: true}, nil,
		} {
			if send, end := gate.LiveGuard(p, c.channel).Event(ctx, c.ev.typ, c.ev.payload); !send || end {
				t.Errorf("%v, %s: send=%v end=%v, want it sent", p, c.name, send, end)
			}
		}
	}

	// a restricted account's limit is read for every event: a change with no
	// notice from anyone holds at the next one
	lg := gate.LiveGuard(child, "library:"+libOpen)
	teenEvent := map[string]any{"video_id": teen}
	if send, _ := lg.Event(ctx, "video.added", teenEvent); send {
		t.Fatal("the 16+ video's event was sent under a 9+ limit")
	}
	e.exec(`UPDATE parental_policies SET max_tier = 16, version = version + 1 WHERE user_id = $1`, uChild)
	if send, end := lg.Event(ctx, "video.added", teenEvent); !send || end {
		t.Errorf("after the limit rose to 16+: send=%v end=%v", send, end)
	}
	e.exec(`INSERT INTO parental_rules (user_id, effect, subject_kind, subject_id) VALUES ($1, 'block', 'video', $2)`, uChild, teen)
	if send, _ := lg.Event(ctx, "video.added", teenEvent); send {
		t.Error("after a block the video's event was still sent")
	}
	e.exec(`INSERT INTO parental_rules (user_id, effect, subject_kind, subject_id) VALUES ($1, 'block', 'library', $2)`, uChild, libOpen)
	if send, end := lg.Event(ctx, "video.added", map[string]any{"video_id": kid}); send || !end {
		t.Errorf("after a block on the feed's library: send=%v end=%v, want the feed ended", send, end)
	}

	// an account with no limit: while the cache does not listen its answer is
	// reused for a moment, and read again at once when a policy changed here
	viewer := &principal.Principal{UserID: uViewer, Libraries: []string{libOpen}}
	vg := gate.LiveGuard(viewer, "home")
	if send, _ := vg.Event(ctx, "video.added", teenEvent); !send {
		t.Fatal("the viewer was not sent the event")
	}
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1)`, uViewer)
	if send, _ := vg.Event(ctx, "video.added", teenEvent); !send {
		t.Error("the unlimited answer was not reused within its moment (a query per event while the cache does not listen)")
	}
	cache.Invalidate(uViewer) // what the parental routes do after a change
	if send, _ := vg.Event(ctx, "video.added", teenEvent); send {
		t.Error("after the policy changed here the event was still sent")
	}
	// a new connection always reads
	if send, _ := gate.LiveGuard(viewer, "home").Event(ctx, "video.added", teenEvent); send {
		t.Error("a new connection of the now restricted account was sent the event")
	}
}
