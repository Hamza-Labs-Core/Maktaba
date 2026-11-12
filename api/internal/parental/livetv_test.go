package parental

import (
	"testing"
	"time"
)

// T01 (story 57.10): a programme's tier is the guide's rating read through the
// tables (XMLTV and Plex write the same labels), else the scheduled video's tier,
// else the tier stored with the guide, else the channel's.
func TestProgrammeTier(t *testing.T) {
	tab := Default()
	n := func(v int) *int { return &v }
	content := func(r string) *int {
		c, ok := tab.Content(r)
		if !ok {
			t.Fatalf("the tables do not know %q", r)
		}
		return &c
	}
	show := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	for _, c := range []struct {
		name                   string
		rating                 string
		video, stored, channel *int
		want                   *int
	}{
		{"an XMLTV rating (MPAA system)", "PG-13", n(0), n(0), n(0), content("PG-13")},
		{"an XMLTV rating (US TV system)", "TV-MA", nil, nil, n(9), content("TV-MA")},
		{"a Plex contentRating", "TV-14", n(0), nil, n(9), content("TV-14")},
		{"a Plex contentRating with its country", "gb/15", nil, nil, n(9), content("gb/15")},
		{"a rating padded with spaces", "  TV-Y  ", nil, nil, n(18), content("TV-Y")},
		{"an unknown rating falls to the video", "Zzz-Unknown", n(13), n(9), n(0), n(13)},
		{"no rating: the video's tier now, ahead of the stored one", "", n(16), n(9), n(0), n(16)},
		{"no rating, no video tier: the stored tier", "", nil, n(13), n(9), n(13)},
		{"nothing but the channel", "", nil, nil, n(9), n(9)},
		{"nothing at all is unrated", "", nil, nil, nil, nil},
	} {
		if got := ProgrammeTier(tab, c.rating, c.video, c.stored, c.channel); show(got) != show(c.want) {
			t.Errorf("%s: tier %d, want %d", c.name, show(got), show(c.want))
		}
	}
	if got := ProgrammeTier(nil, "TV-MA", nil, nil, nil); show(got) != show(content("TV-MA")) {
		t.Errorf("a nil table reads the default tables: %d", show(got))
	}
}

func TestChannelAndProgrammePermitted(t *testing.T) {
	now := time.Now()
	n := func(v int) *int { return &v }
	lib := "11111111-0000-4000-8000-000000000001"
	child := func(rules ...Rule) *Effective {
		return &Effective{Restricted: true, MaxTier: Tier9, Unrated: UnratedHide, Rules: rules}
	}
	ch := func(id string, tier *int) ChannelFacts { return ChannelFacts{ID: id, Tier: tier} }
	for _, c := range []struct {
		name string
		e    *Effective
		c    ChannelFacts
		want bool
	}{
		{"no policy", &Effective{}, ch("c16", n(16)), true},
		{"within the limit", child(), ch("c9", n(9)), true},
		{"above the limit", child(), ch("c16", n(16)), false},
		{"no tier and no library is hidden", child(), ch("none", nil), false},
		{"no tier, unrated treated as 9", &Effective{Restricted: true, MaxTier: Tier9, Unrated: "tier_9"}, ch("none", nil), true},
		{"allowed by a rule", child(Rule{Effect: EffectAllow, SubjectKind: SubjectChannel, SubjectID: "c16"}), ch("c16", n(16)), true},
		{"blocked by a rule", child(Rule{Effect: EffectBlock, SubjectKind: SubjectChannel, SubjectID: "c9"}), ch("c9", n(9)), false},
		{"its library blocked", child(Rule{Effect: EffectBlock, SubjectKind: SubjectLibrary, SubjectID: lib}),
			ChannelFacts{ID: "c9", Tier: n(9), LibraryID: &lib}, false},
	} {
		if got := ChannelPermitted(c.e, c.c, now); got != c.want {
			t.Errorf("channel %s: %v, want %v", c.name, got, c.want)
		}
	}
	nine := ch("c9", n(9))
	for _, c := range []struct {
		name string
		e    *Effective
		p    ProgrammeFacts
		want bool
	}{
		{"takes the channel's tier", child(), ProgrammeFacts{}, true},
		{"a 16 rating on a 9 channel", child(), ProgrammeFacts{Rating: "TV-MA"}, false},
		{"a 16 video on a 9 channel", child(), ProgrammeFacts{VideoID: "v", VideoTier: n(16)}, false},
		{"the stored tier", child(), ProgrammeFacts{StoredTier: n(16)}, false},
		{"a blocked video within the limit", child(Rule{Effect: EffectBlock, SubjectKind: SubjectVideo, SubjectID: "v"}),
			ProgrammeFacts{VideoID: "v", VideoTier: n(0)}, false},
		{"a blocked series", child(Rule{Effect: EffectBlock, SubjectKind: SubjectSeries, SubjectID: "s"}),
			ProgrammeFacts{VideoID: "v", VideoTier: n(0), SeriesIDs: []string{"s"}}, false},
		{"no policy", &Effective{}, ProgrammeFacts{Rating: "TV-MA"}, true},
	} {
		if got := ProgrammePermitted(c.e, Default(), nine, c.p, now); got != c.want {
			t.Errorf("programme %s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProgrammeVerdicts(t *testing.T) {
	t0 := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)
	pv := &ProgrammeVerdicts{
		bySeq: map[string]map[int64]bool{"c": {1: true, 2: false}},
		spans: map[string][]verdictSpan{"c": {{t0, t0.Add(time.Hour), true}, {t0.Add(time.Hour), t0.Add(2 * time.Hour), false}}},
	}
	if !pv.Allowed("C", 1) || pv.Allowed("c", 2) {
		t.Error("the stored verdicts are not answered")
	}
	if pv.Allowed("c", 3) || pv.Allowed("other", 1) {
		t.Error("a programme nobody judged is allowed")
	}
	if !pv.AllowedAt("c", t0.Add(59*time.Minute)) || pv.AllowedAt("c", t0.Add(time.Hour)) {
		t.Error("the boundary is not at the programme's start")
	}
	if !pv.AllowedAt("c", t0.Add(3*time.Hour)) {
		t.Error("a gap in the schedule (the channel's own filler) is refused")
	}
	all := &ProgrammeVerdicts{all: true}
	if !all.Allowed("x", 9) || !all.AllowedAt("x", t0) {
		t.Error("an unrestricted account is refused")
	}
}
