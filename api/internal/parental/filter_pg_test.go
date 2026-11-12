package parental_test

// Story 57.03 T01: the filter's SQL and its single-object check against
// Effective.Permits, over a table of policies and rules, on a real migrated
// Postgres. Runs when MAKTABA_TEST_PG_URL is set.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

type fVideo struct {
	id, lib          string
	tier             int // -1: unrated
	series, col, src string
}

const (
	fSeries = "00000000-0000-4000-8000-0000005703a1"
	fCol    = "00000000-0000-4000-8000-0000005703b1"
	fSmart  = "00000000-0000-4000-8000-0000005703b2"
	fEmpty  = "00000000-0000-4000-8000-0000005703b3"
	fSrc    = "00000000-0000-4000-8000-0000005703c1"
)

func fID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-0000057030%02d", n) }

func seedFilter(e *env) []fVideo {
	e.t.Helper()
	vs := []fVideo{
		{id: fID(1), lib: libOpen, tier: 0},
		{id: fID(2), lib: libOpen, tier: 9},
		{id: fID(3), lib: libOpen, tier: 13},
		{id: fID(4), lib: libOpen, tier: 16},
		{id: fID(5), lib: libOpen, tier: 18},
		{id: fID(6), lib: libOpen, tier: -1},
		{id: fID(7), lib: libOpen, tier: 9, series: fSeries},
		{id: fID(8), lib: libOpen, tier: 16, series: fSeries},
		{id: fID(9), lib: libOpen, tier: 16, col: fCol},
		{id: fID(10), lib: libOpen, tier: 13, src: fSrc},
		{id: fID(11), lib: libClosed, tier: 9},
		{id: fID(12), lib: libClosed, tier: -1, col: fCol},
	}
	e.exec(`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'show')`, fSeries, libOpen)
	e.exec(`INSERT INTO collections (id, library_id, name) VALUES ($1, $2, 'picks'), ($3, $2, 'none')`, fCol, libOpen, fEmpty)
	e.exec(`INSERT INTO collections (id, library_id, name, is_smart, smart_query) VALUES ($1, $2, 'smart', true, '{}')`, fSmart, libOpen)
	e.exec(`INSERT INTO youtube_sources (id, library_id, kind, url, external_id) VALUES ($1, $2, 'channel', 'https://example.test/f', 'UC5703')`, fSrc, libOpen)
	for i, v := range vs {
		e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
			VALUES ($1::uuid, $2::uuid, '/media/f/' || $1::text || '.mp4', $1::text || '.mp4',
			        encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), 1000, now(), 'ready', 't', 100, now(), now())`, v.id, v.lib)
		if v.tier >= 0 {
			e.exec(`UPDATE videos SET age_tier = $2 WHERE id = $1`, v.id, v.tier)
		}
		if v.series != "" {
			e.exec(`INSERT INTO series_episodes (series_id, video_id) VALUES ($1, $2)`, v.series, v.id)
		}
		if v.col != "" {
			e.exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $2, $3)`, v.col, v.id, i)
		}
		if v.src != "" {
			e.exec(`INSERT INTO youtube_downloads (library_id, youtube_id, source_id, video_id) VALUES ($1, $2, $3, $4)`, v.lib, "yt"+v.id[30:], v.src, v.id)
		}
	}
	return vs
}

func (v fVideo) item() parental.Item {
	it := parental.Item{Subjects: []parental.Subject{{Kind: parental.SubjectVideo, ID: v.id}, {Kind: parental.SubjectLibrary, ID: v.lib}}}
	if v.tier >= 0 {
		t := v.tier
		it.Tier = &t
	}
	if v.series != "" {
		it.Subjects = append(it.Subjects, parental.Subject{Kind: parental.SubjectSeries, ID: v.series})
	}
	if v.col != "" {
		it.Subjects = append(it.Subjects, parental.Subject{Kind: parental.SubjectCollection, ID: v.col})
	}
	if v.src != "" {
		it.Subjects = append(it.Subjects, parental.Subject{Kind: parental.SubjectYouTubeSource, ID: v.src})
	}
	return it
}

func fPolicies() map[string]*parental.Effective {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	rule := func(effect, kind, id string, exp *time.Time) parental.Rule {
		return parental.Rule{Effect: effect, SubjectKind: kind, SubjectID: id, ExpiresAt: exp}
	}
	lim := func(max int, unrated string, only bool, rules ...parental.Rule) *parental.Effective {
		return &parental.Effective{UserID: uChild, Restricted: true, MaxTier: max, Unrated: unrated, AllowOnly: only, Rules: rules}
	}
	const A, B = parental.EffectAllow, parental.EffectBlock
	out := map[string]*parental.Effective{
		"no limit":                    {UserID: uViewer},
		"all ages, unrated hidden":    lim(0, "hide", false),
		"9+, unrated hidden":          lim(9, "hide", false),
		"13+, unrated as 9+":          lim(13, "tier_9", false),
		"9+, unrated as 13+":          lim(9, "tier_13", false),
		"18+, unrated as all ages":    lim(18, "tier_0", false),
		"nothing allowed":             lim(parental.NothingAllowed, "hide", false),
		"an unknown unrated rule":     lim(18, "whatever", false),
		"allow a 16+ video":           lim(9, "hide", false, rule(A, parental.SubjectVideo, fID(4), nil)),
		"allow an unrated video":      lim(9, "hide", false, rule(A, parental.SubjectVideo, fID(6), nil)),
		"allow a series":              lim(0, "hide", false, rule(A, parental.SubjectSeries, fSeries, nil)),
		"allow a collection":          lim(0, "hide", false, rule(A, parental.SubjectCollection, fCol, nil)),
		"allow a YouTube source":      lim(0, "hide", false, rule(A, parental.SubjectYouTubeSource, fSrc, nil)),
		"allow a library":             lim(0, "hide", false, rule(A, parental.SubjectLibrary, libClosed, nil)),
		"block a video under limit":   lim(18, "tier_0", false, rule(B, parental.SubjectVideo, fID(1), nil)),
		"block a series":              lim(18, "tier_0", false, rule(B, parental.SubjectSeries, fSeries, nil)),
		"block a collection":          lim(18, "tier_0", false, rule(B, parental.SubjectCollection, fCol, nil)),
		"block a YouTube source":      lim(18, "tier_0", false, rule(B, parental.SubjectYouTubeSource, fSrc, nil)),
		"block a library":             lim(18, "tier_0", false, rule(B, parental.SubjectLibrary, libClosed, nil)),
		"block beats allow":           lim(9, "hide", false, rule(A, parental.SubjectVideo, fID(8), nil), rule(B, parental.SubjectSeries, fSeries, nil)),
		"block beats allow, same id":  lim(18, "tier_0", false, rule(A, parental.SubjectVideo, fID(2), nil), rule(B, parental.SubjectVideo, fID(2), nil)),
		"an expired allow":            lim(9, "hide", false, rule(A, parental.SubjectVideo, fID(4), &past)),
		"an expired block":            lim(18, "tier_0", false, rule(B, parental.SubjectVideo, fID(1), &past)),
		"an allow not yet run out":    lim(9, "hide", false, rule(A, parental.SubjectVideo, fID(5), &future)),
		"allow only, nothing allowed": lim(18, "tier_0", true),
		"allow only, one video":       lim(18, "tier_0", true, rule(A, parental.SubjectVideo, fID(3), nil)),
		"allow only, a series, a block inside": lim(0, "hide", true, rule(A, parental.SubjectSeries, fSeries, nil),
			rule(B, parental.SubjectVideo, fID(7), nil)),
		"a rule on an id that is no uuid": lim(9, "hide", false, rule(A, parental.SubjectVideo, "x') OR TRUE --", nil),
			rule(B, parental.SubjectVideo, "v-blocked", nil)),
	}
	return out
}

func TestFilterSQLMatchesPermits(t *testing.T) {
	e := setup(t)
	vs := seedFilter(e)
	ctx := context.Background()
	now := time.Now()
	for name, eff := range fPolicies() {
		var want []string
		for _, v := range vs {
			if eff.Permits(v.item(), now) {
				want = append(want, v.id)
			}
		}
		// the clause, in a query that has its own placeholder
		rows, err := e.db.QueryContext(ctx, `SELECT v.id::text FROM videos v WHERE v.state = $1`+parental.SQL(eff, "v")+` ORDER BY v.id`, "ready")
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, parental.SQL(eff, "v"))
		}
		var got []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		_ = rows.Close()
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: the query shows %v, Permits shows %v\n%s", name, got, want, parental.SQL(eff, "v"))
		}
		ids := make([]string, 0, len(vs))
		for _, v := range vs {
			ids = append(ids, v.id)
			ok, err := parental.AllowedVideo(ctx, e.db, eff, v.id)
			if err != nil || ok != eff.Permits(v.item(), now) {
				t.Errorf("%s: AllowedVideo(%s) = %v (%v), Permits = %v", name, v.id, ok, err, eff.Permits(v.item(), now))
			}
		}
		set, err := parental.AllowedVideos(ctx, e.db, eff, ids)
		if err != nil || len(set) != len(want) {
			t.Errorf("%s: AllowedVideos = %d ids (%v), want %d", name, len(set), err, len(want))
		}
		for _, id := range want {
			if !set[id] {
				t.Errorf("%s: AllowedVideos leaves out %s", name, id)
			}
		}
	}
	if got := parental.SQL(&parental.Effective{}, "v"); got != "" {
		t.Errorf("no limit adds %q to a query, want nothing", got)
	}
	if got := parental.SQL(nil, "v"); got != "" {
		t.Errorf("a nil limit adds %q to a query, want nothing", got)
	}
}

// The single-object check for what is not a video, and what it refuses outright.
func TestFilterAllowed(t *testing.T) {
	e := setup(t)
	seedFilter(e)
	ctx := context.Background()
	ps := fPolicies()
	missing := "00000000-0000-4000-8000-0000005703ff"
	cases := []struct {
		policy, kind, id string
		want             bool
	}{
		{"no limit", parental.SubjectSeries, fSeries, true},
		{"no limit", parental.SubjectVideo, "not-an-id", true},               // no limit: nothing is checked
		{"9+, unrated hidden", parental.SubjectSeries, fSeries, true},        // one episode passes
		{"all ages, unrated hidden", parental.SubjectSeries, fSeries, false}, // none does
		{"block a series", parental.SubjectSeries, fSeries, false},
		{"allow a series", parental.SubjectSeries, fSeries, true},
		{"allow only, nothing allowed", parental.SubjectSeries, fSeries, false},
		{"9+, unrated hidden", parental.SubjectSeries, missing, true}, // nothing to hide
		{"9+, unrated hidden", parental.SubjectCollection, fCol, false},
		{"18+, unrated as all ages", parental.SubjectCollection, fCol, true},
		{"block a collection", parental.SubjectCollection, fCol, false},
		{"allow a collection", parental.SubjectCollection, fCol, true},
		{"9+, unrated hidden", parental.SubjectCollection, fEmpty, false}, // nothing in it passes
		{"9+, unrated hidden", parental.SubjectCollection, fSmart, true},  // its list is filtered instead
		{"9+, unrated hidden", parental.SubjectCollection, missing, true},
		{"9+, unrated hidden", parental.SubjectLibrary, libOpen, true},
		{"block a library", parental.SubjectLibrary, libClosed, false},
		{"block a library", parental.SubjectLibrary, strings.ToUpper(libClosed), false},
		{"9+, unrated hidden", parental.SubjectVideo, missing, true},
		{"9+, unrated hidden", parental.SubjectVideo, "not-an-id", false},
		{"9+, unrated hidden", parental.SubjectVideo, strings.ReplaceAll(fID(5), "-", ""), false}, // another spelling of a hidden id
		{"9+, unrated hidden", parental.SubjectVideo, "{" + fID(2) + "}", true},
		{"9+, unrated hidden", "plugin", missing, false}, // a kind the filter does not know
	}
	for _, c := range cases {
		got, err := parental.Allowed(ctx, e.db, ps[c.policy], c.kind, c.id)
		if err != nil || got != c.want {
			t.Errorf("%s: Allowed(%s %s) = %v (%v), want %v", c.policy, c.kind, c.id, got, err, c.want)
		}
	}

	// a rule stored with another spelling of the id still names it
	upper := &parental.Effective{UserID: uChild, Restricted: true, MaxTier: 18, Unrated: "tier_0",
		Rules: []parental.Rule{{Effect: parental.EffectBlock, SubjectKind: parental.SubjectSeries, SubjectID: strings.ToUpper(fSeries)}}}
	if ok, err := parental.Allowed(ctx, e.db, upper, parental.SubjectSeries, fSeries); err != nil || ok {
		t.Errorf("a block stored in upper case: Allowed = %v (%v), want false", ok, err)
	}

	// fail closed: a query that cannot run refuses
	closed, cancel := context.WithCancel(ctx)
	cancel()
	nine := ps["9+, unrated hidden"]
	if ok, err := parental.Allowed(closed, e.db, nine, parental.SubjectVideo, fID(1)); ok || err == nil {
		t.Errorf("Allowed on a dead context = %v (%v), want false and an error", ok, err)
	}
	if set, err := parental.AllowedVideos(closed, e.db, nine, []string{fID(1)}); len(set) != 0 || err == nil {
		t.Errorf("AllowedVideos on a dead context = %v (%v), want none and an error", set, err)
	}

	// Check reads the request's limit and tells the hook of each refusal
	var told []parental.Subject
	parental.SetBlockedHook(func(_ context.Context, _ *parental.Effective, s parental.Subject) { told = append(told, s) })
	defer parental.SetBlockedHook(nil)
	if !parental.Check(ctx, e.db, parental.SubjectVideo, fID(5)) {
		t.Error("Check with no limit in the context refused")
	}
	lctx := parental.With(ctx, nine)
	if !parental.Check(lctx, e.db, parental.SubjectVideo, fID(2)) || parental.Check(lctx, e.db, parental.SubjectVideo, fID(5)) {
		t.Error("Check under a 9+ limit: want the 9+ video shown and the 18+ video refused")
	}
	if parental.Check(parental.With(closed, nine), e.db, parental.SubjectVideo, fID(2)) {
		t.Error("Check on a dead context allowed")
	}
	if len(told) != 2 || told[0].ID != fID(5) || told[0].Kind != parental.SubjectVideo {
		t.Errorf("the blocked hook was told %v, want the two refusals", told)
	}
	if parental.From(ctx).Limits() || !parental.From(lctx).Limits() || parental.Where(ctx, "v") != "" || parental.Where(lctx, "v") == "" {
		t.Error("From / Where do not follow the context's limit")
	}
}
