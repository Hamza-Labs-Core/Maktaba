package parental

// Story 57.03, test case T07: the live feeds' filter, the parts that need no
// database: what a payload names, and that it fails closed. The feeds themselves
// are tested behind the router (api/internal/router/p57_gaps_pg_test.go).

import (
	"context"
	"sort"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func TestLiveSubjects(t *testing.T) {
	const a, b, c = "00000000-0000-4000-8000-00000000000a", "00000000-0000-4000-8000-00000000000b", "00000000-0000-4000-8000-00000000000c"
	cases := []struct {
		name    string
		payload map[string]any
		want    []string
		sound   bool
	}{
		{"nothing", map[string]any{"etag": "x"}, nil, true},
		{"nil", nil, nil, true},
		{"a video", map[string]any{"video_id": a}, []string{"video " + a}, true},
		{"many videos", map[string]any{"video_ids": []any{a, b}}, []string{"video " + a, "video " + b}, true},
		{"typed list", map[string]any{"video_ids": []string{a}}, []string{"video " + a}, true},
		{"a series, a collection, a library, a channel",
			map[string]any{"series_id": a, "collection_id": b, "library_id": c, "channel_id": a},
			[]string{"channel " + a, "collection " + b, "library " + c, "series " + a}, true},
		{"a video object", map[string]any{"video": map[string]any{"id": a, "library_id": c}}, []string{"library " + c, "video " + a}, true},
		{"items", map[string]any{"items": []any{map[string]any{"video_id": a}, map[string]any{"series_id": b}}}, []string{"series " + b, "video " + a}, true},
		{"videos as ids", map[string]any{"videos": []any{a, b}}, []string{"video " + a, "video " + b}, true},
		{"a job's subject", map[string]any{"job": map[string]any{"subject_kind": "video", "subject_id": a, "library_id": c}}, []string{"library " + c, "video " + a}, true},
		{"empty id", map[string]any{"video_id": ""}, nil, true},
		{"not an id", map[string]any{"video_id": "abc"}, nil, false},
		{"not a string", map[string]any{"video_id": 7}, nil, false},
		{"a bad id in a list", map[string]any{"video_ids": []any{a, 3}}, []string{"video " + a}, false},
		{"a subject kind the filter does not know", map[string]any{"subject_kind": "file", "subject_id": "/m/a.mp4"}, nil, false},
		{"items that are not objects", map[string]any{"items": []any{"x"}}, nil, false},
	}
	for _, tc := range cases {
		got, sound := liveSubjects(tc.payload)
		var names []string
		for _, s := range got {
			names = append(names, s.Kind+" "+s.ID)
		}
		sort.Strings(names)
		if sound != tc.sound || len(names) != len(tc.want) {
			t.Errorf("%s: %v sound=%v, want %v sound=%v", tc.name, names, sound, tc.want, tc.sound)
			continue
		}
		for i := range names {
			if names[i] != tc.want[i] {
				t.Errorf("%s: %v, want %v", tc.name, names, tc.want)
				break
			}
		}
	}
}

// With no policy cache nothing can be decided for a signed-in account: no event
// is sent and the feed ends. A caller with no account (the admin token) has no
// limit.
func TestLiveGuardFailsClosed(t *testing.T) {
	g := NewGate()
	user := &principal.Principal{UserID: "00000000-0000-4000-8000-000000000001"}
	for _, ch := range []string{"home", "library:00000000-0000-4000-8000-00000000000a", "playback:00000000-0000-4000-8000-00000000000b", "jobs"} {
		if send, end := g.LiveGuard(user, ch).Event(context.Background(), "capabilities_changed", nil); send || !end {
			t.Errorf("%s with no cache: send=%v end=%v, want nothing sent and the feed ended", ch, send, end)
		}
	}
	if send, end := g.LiveGuard(&principal.Principal{IsAdmin: true}, "home").Event(context.Background(), "x", nil); !send || end {
		t.Errorf("the admin token: send=%v end=%v", send, end)
	}
	var none *LiveGuard
	if send, end := none.Event(context.Background(), "x", nil); send || !end {
		t.Errorf("no guard: send=%v end=%v", send, end)
	}
}
