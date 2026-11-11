package parental

import (
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
		return &Effective{Restricted: true, MaxTier: PolicyTier9, Unrated: UnratedHide, Rules: rules}
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
			&Effective{Restricted: true, MaxTier: PolicyTier9, Unrated: "tier_9"}, Item{}, true},
		{"unrated treated as 13+ under a 9+ limit",
			&Effective{Restricted: true, MaxTier: PolicyTier9, Unrated: "tier_13"}, Item{}, false},
		{"allow on an unrated title", nine(rule(EffectAllow, vid("v1"), nil)),
			Item{Subjects: []Subject{vid("v1")}}, true},
		{"allow_only hides what is not allowed",
			&Effective{Restricted: true, MaxTier: PolicyTier18, Unrated: "tier_0", AllowOnly: true},
			Item{Tier: tier(0), Subjects: []Subject{vid("v1")}}, false},
		{"allow_only shows what is allowed",
			&Effective{Restricted: true, MaxTier: PolicyTierNone, Unrated: UnratedHide, AllowOnly: true,
				Rules: []Rule{rule(EffectAllow, vid("v1"), nil)}},
			Item{Tier: tier(18), Subjects: []Subject{vid("v1")}}, true},
		{"limit of nothing hides an all-ages title",
			&Effective{Restricted: true, MaxTier: PolicyTierNone, Unrated: UnratedHide}, Item{Tier: tier(0)}, false},
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
		if !ValidPolicyTier(n) {
			t.Errorf("tier %d should be valid", n)
		}
	}
	for _, n := range []int{-2, 1, 12, 17, 21} {
		if ValidPolicyTier(n) {
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
