package parental

import (
	"strings"
	"testing"
	"time"
)

func tier(n int) *int { return &n }

// T03: block beats allow; an allow shows a title above the limit; allow_only
// hides everything not allowed; an expired rule is ignored; unrated follows the
// policy's unrated rule.
func TestPermits(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	vid := func(id string) Subject { return Subject{Kind: SubjectVideo, ID: id} }
	ser := func(id string) Subject { return Subject{Kind: SubjectSeries, ID: id} }
	rule := func(effect string, s Subject, exp *time.Time) Rule {
		return Rule{Effect: effect, SubjectKind: s.Kind, SubjectID: s.ID, ExpiresAt: exp}
	}
	nine := func(rules ...Rule) *Effective {
		return &Effective{Restricted: true, MaxTier: Tier9, Unrated: UnratedHide, Rules: rules}
	}
	cases := []struct {
		name string
		e    *Effective
		item Item
		want bool
	}{
		{"no policy shows everything", &Effective{}, Item{Tier: tier(18)}, true},
		{"nil policy shows everything", nil, Item{Tier: tier(18)}, true},
		{"at the limit", nine(), Item{Tier: tier(9)}, true},
		{"above the limit", nine(), Item{Tier: tier(13)}, false},
		{"allow on a 16+ title under a 9+ limit", nine(rule(EffectAllow, vid("v1"), nil)),
			Item{Tier: tier(16), Subjects: []Subject{vid("v1")}}, true},
		{"allow on its series shows it", nine(rule(EffectAllow, ser("s1"), nil)),
			Item{Tier: tier(16), Subjects: []Subject{vid("v1"), ser("s1")}}, true},
		{"block wins over allow on the same subject",
			nine(rule(EffectAllow, vid("v1"), nil), rule(EffectBlock, vid("v1"), nil)),
			Item{Tier: tier(0), Subjects: []Subject{vid("v1")}}, false},
		{"block on the series wins over allow on the video",
			nine(rule(EffectAllow, vid("v1"), nil), rule(EffectBlock, ser("s1"), nil)),
			Item{Tier: tier(0), Subjects: []Subject{vid("v1"), ser("s1")}}, false},
		{"block hides a title under the limit", nine(rule(EffectBlock, vid("v1"), nil)),
			Item{Tier: tier(0), Subjects: []Subject{vid("v1")}}, false},
		{"expired allow is ignored", nine(rule(EffectAllow, vid("v1"), &past)),
			Item{Tier: tier(16), Subjects: []Subject{vid("v1")}}, false},
		{"expired block is ignored", nine(rule(EffectBlock, vid("v1"), &past)),
			Item{Tier: tier(0), Subjects: []Subject{vid("v1")}}, true},
		{"an allow that has not run out holds", nine(rule(EffectAllow, vid("v1"), &future)),
			Item{Tier: tier(16), Subjects: []Subject{vid("v1")}}, true},
		{"unrated hidden by default", nine(), Item{}, false},
		{"unrated treated as 9+ under a 9+ limit",
			&Effective{Restricted: true, MaxTier: Tier9, Unrated: "tier_9"}, Item{}, true},
		{"unrated treated as 13+ under a 9+ limit",
			&Effective{Restricted: true, MaxTier: Tier9, Unrated: "tier_13"}, Item{}, false},
		{"allow on an unrated title", nine(rule(EffectAllow, vid("v1"), nil)),
			Item{Subjects: []Subject{vid("v1")}}, true},
		{"allow_only hides what is not allowed",
			&Effective{Restricted: true, MaxTier: Tier18, Unrated: "tier_0", AllowOnly: true},
			Item{Tier: tier(0), Subjects: []Subject{vid("v1")}}, false},
		{"allow_only shows what is allowed",
			&Effective{Restricted: true, MaxTier: NothingAllowed, Unrated: UnratedHide, AllowOnly: true,
				Rules: []Rule{rule(EffectAllow, vid("v1"), nil)}},
			Item{Tier: tier(18), Subjects: []Subject{vid("v1")}}, true},
		{"limit of nothing hides an all-ages title",
			&Effective{Restricted: true, MaxTier: NothingAllowed, Unrated: UnratedHide}, Item{Tier: tier(0)}, false},
	}
	for _, c := range cases {
		if got := c.e.Permits(c.item, now); got != c.want {
			t.Errorf("%s: Permits = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSubjectsSkipsExpiredAndOtherKinds(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Second)
	e := &Effective{Restricted: true, Rules: []Rule{
		{Effect: EffectBlock, SubjectKind: SubjectLibrary, SubjectID: "a"},
		{Effect: EffectBlock, SubjectKind: SubjectLibrary, SubjectID: "b", ExpiresAt: &past},
		{Effect: EffectAllow, SubjectKind: SubjectLibrary, SubjectID: "c"},
		{Effect: EffectBlock, SubjectKind: SubjectSeries, SubjectID: "d"},
	}}
	got := e.Subjects(EffectBlock, SubjectLibrary, now)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("Subjects = %v, want [a]", got)
	}
	if (&Effective{}).Subjects(EffectBlock, SubjectLibrary, now) != nil {
		t.Fatal("an unrestricted account has no subjects")
	}
}

func TestValidators(t *testing.T) {
	for _, n := range []int{-1, 0, 9, 13, 16, 18} {
		if !ValidPolicyLimit(n) {
			t.Errorf("tier %d should be valid", n)
		}
	}
	for _, n := range []int{-2, 1, 12, 17, 21} {
		if ValidPolicyLimit(n) {
			t.Errorf("tier %d should not be valid", n)
		}
	}
	if !ValidUnrated("hide") || !ValidUnrated("tier_13") || ValidUnrated("tier_12") || ValidUnrated("") {
		t.Error("ValidUnrated")
	}
	if !ValidSubjectKind(SubjectYouTubeSource) || ValidSubjectKind("user") {
		t.Error("ValidSubjectKind")
	}
}

// Review of the filter, finding 6: a rule's uuid matches whatever spelling it was
// stored in. A block stored in capitals once never matched the lower-case id live TV
// asks about, and the channel stayed in the child's guide.
func TestRulesMatchAnySpellingOfAUUID(t *testing.T) {
	const id = "ab120000-0000-4000-8000-00000000cd34"
	now := time.Now()
	spellings := []string{id, strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), "{" + strings.ToUpper(id) + "}"}
	for _, stored := range spellings {
		for _, asked := range spellings {
			block := &Effective{Restricted: true, MaxTier: Tier18, Rules: []Rule{{Effect: EffectBlock, SubjectKind: SubjectChannel, SubjectID: stored}}}
			s := Subject{Kind: SubjectChannel, ID: asked}
			if !block.Blocked(s, now) {
				t.Errorf("a block stored as %q does not name %q", stored, asked)
			}
			if block.Permits(Item{Subjects: []Subject{s}}, now) {
				t.Errorf("a block stored as %q permits %q", stored, asked)
			}
			// live TV asks with the channel row's own id
			if !ChannelPermitted(block, ChannelFacts{ID: "ab120000-0000-4000-8000-00000000cd35", Tier: tier(0)}, now) {
				t.Fatal("a channel no rule names is not permitted: the check below would prove nothing")
			}
			if ChannelPermitted(block, ChannelFacts{ID: id, Tier: tier(0)}, now) {
				t.Errorf("live TV permits the channel under a block stored as %q", stored)
			}
			allow := &Effective{Restricted: true, MaxTier: NothingAllowed, AllowOnly: true, Rules: []Rule{{Effect: EffectAllow, SubjectKind: SubjectVideo, SubjectID: stored}}}
			v := Subject{Kind: SubjectVideo, ID: asked}
			if !allow.AllowedBy(v, now) || !allow.Permits(Item{Subjects: []Subject{v}}, now) {
				t.Errorf("an allow stored as %q does not name %q", stored, asked)
			}
		}
	}
	// ids that are not uuids are compared as written, and another uuid never matches
	e := &Effective{Restricted: true, Rules: []Rule{
		{Effect: EffectBlock, SubjectKind: SubjectPlugin, SubjectID: "Some.Plugin"},
		{Effect: EffectBlock, SubjectKind: SubjectVideo, SubjectID: id},
	}}
	if !e.Blocked(Subject{Kind: SubjectPlugin, ID: "Some.Plugin"}, now) || e.Blocked(Subject{Kind: SubjectPlugin, ID: "some.plugin"}, now) {
		t.Error("an id that is not a uuid is not compared as written")
	}
	if e.Blocked(Subject{Kind: SubjectVideo, ID: "ab120000-0000-4000-8000-00000000cd35"}, now) || e.Blocked(Subject{Kind: SubjectChannel, ID: id}, now) {
		t.Error("a rule names another id or another kind")
	}
}
