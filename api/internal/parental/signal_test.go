package parental

import (
	"testing"
	"time"
)

// T03: every step of the Apple scales and every age range, to its limit. A limit
// rounds down: R (17) is a 16+ limit, TV-14 a 13+ one.
func TestSignalMapping(t *testing.T) {
	tab := Default()
	scale := []struct {
		system string
		value  int
		want   int
	}{
		{SystemAppleMovie, 0, NothingAllowed},
		{SystemAppleMovie, 100, TierAll}, // G
		{SystemAppleMovie, 200, Tier9},   // PG
		{SystemAppleMovie, 300, Tier13},  // PG-13
		{SystemAppleMovie, 400, Tier16},  // R (17) rounds down
		{SystemAppleMovie, 500, Tier18},  // NC-17
		{SystemAppleMovie, 1000, NoLimit},
		{SystemAppleTV, 0, NothingAllowed},
		{SystemAppleTV, 100, TierAll}, // TV-Y
		{SystemAppleTV, 200, TierAll}, // TV-Y7 (7) rounds down
		{SystemAppleTV, 300, TierAll}, // TV-G
		{SystemAppleTV, 400, Tier9},   // TV-PG
		{SystemAppleTV, 500, Tier13},  // TV-14 rounds down
		{SystemAppleTV, 600, Tier16},  // TV-MA (17) rounds down
		{SystemAppleTV, 1000, NoLimit},
		// between two steps: the step below
		{SystemAppleMovie, 350, Tier13},
		{SystemAppleMovie, 999, Tier18},
		{SystemAppleTV, 50, NothingAllowed},
		{SystemAppleTV, 450, Tier9},
	}
	for _, c := range scale {
		got, ok := tab.ScaleLimit(c.system, c.value)
		if !ok || got != c.want {
			t.Errorf("ScaleLimit(%s, %d) = %d, %v; want %d", c.system, c.value, got, ok, c.want)
		}
	}
	if _, ok := tab.ScaleLimit("mpaa", 300); ok {
		t.Error("a system with no scale gave a limit")
	}
	if _, ok := tab.ScaleLimit(SystemAppleMovie, -5); ok {
		t.Error("a value under the scale gave a limit")
	}

	ages := []struct {
		r    *AgeRange
		want int
	}{
		{nil, NoLimit},
		{&AgeRange{}, NoLimit},
		{&AgeRange{Lower: ip(18)}, NoLimit}, // 18 and over
		{&AgeRange{Lower: ip(0), Upper: ip(4)}, TierAll},
		{&AgeRange{Lower: ip(5), Upper: ip(9)}, TierAll},
		{&AgeRange{Lower: ip(9), Upper: ip(12)}, Tier9},
		{&AgeRange{Lower: ip(10), Upper: ip(13)}, Tier9},
		{&AgeRange{Lower: ip(13), Upper: ip(15)}, Tier13},
		{&AgeRange{Lower: ip(16), Upper: ip(17)}, Tier13},
		{&AgeRange{Lower: ip(16), Upper: ip(18)}, Tier13},
		{&AgeRange{Lower: ip(18), Upper: ip(25)}, NoLimit},
	}
	for _, c := range ages {
		if got := AgeRangeLimit(c.r); got != c.want {
			t.Errorf("AgeRangeLimit(%+v) = %d, want %d", c.r, got, c.want)
		}
	}
}

// T04 (the mapping): the strictest of the three decides.
func TestSignalLimitIsTheStrictest(t *testing.T) {
	tab := Default()
	cases := []struct {
		name string
		in   SignalInput
		want int
	}{
		{"nothing said", SignalInput{}, NoLimit},
		{"range 13-15, movies R, TV TV-PG", SignalInput{AgeRange: &AgeRange{Lower: ip(13), Upper: ip(15)}, MovieCeiling: ip(400), TVCeiling: ip(400)}, Tier9},
		{"range 9-12, no ceilings", SignalInput{AgeRange: &AgeRange{Lower: ip(9), Upper: ip(12)}}, Tier9},
		{"adult, movies PG-13", SignalInput{AgeRange: &AgeRange{Lower: ip(18)}, MovieCeiling: ip(300), TVCeiling: ip(1000)}, Tier13},
		{"adult, no limits", SignalInput{AgeRange: &AgeRange{Lower: ip(18)}, MovieCeiling: ip(1000), TVCeiling: ip(1000)}, NoLimit},
		{"TV off", SignalInput{MovieCeiling: ip(500), TVCeiling: ip(0)}, NothingAllowed},
	}
	for _, c := range cases {
		if got := tab.SignalLimit(c.in); got != c.want {
			t.Errorf("%s: SignalLimit = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestSignalInputNormalize(t *testing.T) {
	good := SignalInput{AgeRange: &AgeRange{Lower: ip(9), Upper: ip(12), Declaration: "guardian_declared"},
		MovieCeiling: ip(300), TVCeiling: ip(1000), Region: " sa "}
	if p := good.Normalize(); len(p) != 0 || good.Region != "SA" {
		t.Fatalf("good input: %v, region %q", p, good.Region)
	}
	bad := []struct {
		field string
		in    SignalInput
	}{
		{"age_range.lower", SignalInput{AgeRange: &AgeRange{Lower: ip(-1)}}},
		{"age_range.upper", SignalInput{AgeRange: &AgeRange{Upper: ip(151)}}},
		{"age_range.upper", SignalInput{AgeRange: &AgeRange{Lower: ip(13), Upper: ip(9)}}},
		{"age_range.declaration", SignalInput{AgeRange: &AgeRange{Declaration: "Self Declared"}}},
		{"movie_ceiling", SignalInput{MovieCeiling: ip(1001)}},
		{"tv_ceiling", SignalInput{TVCeiling: ip(-1)}},
		{"region", SignalInput{Region: "United States"}},
	}
	for _, c := range bad {
		p := c.in.Normalize()
		if len(p) != 1 || p[0].Field != c.field {
			t.Errorf("%+v: problems %v, want one on %s", c.in, p, c.field)
		}
	}
}

// The claim is read toward hiding, and a signal folds into a policy as the
// stricter of the two.
func TestClaimLimitAndWithSignal(t *testing.T) {
	for _, c := range []struct {
		ac   *int
		want int
	}{{nil, NoLimit}, {ip(-1), NothingAllowed}, {ip(-7), NothingAllowed}, {ip(0), TierAll}, {ip(9), Tier9},
		{ip(12), Tier9}, {ip(17), Tier16}, {ip(18), Tier18}, {ip(1000), NoLimit}} {
		if got := ClaimLimit(c.ac); got != c.want {
			t.Errorf("ClaimLimit(%v) = %d, want %d", c.ac, got, c.want)
		}
	}

	sixteen, nine := 16, 9
	child := &Effective{UserID: "c", Restricted: true, MaxTier: Tier13, Unrated: "tier_9", Version: 3,
		Rules: []Rule{{Effect: EffectAllow, SubjectKind: SubjectVideo, SubjectID: "v-allowed"}}}

	// no signal: the same value
	if got := child.WithSignal(SignInSignal{Limit: NoLimit}); got != child {
		t.Error("no signal changed the policy")
	}
	// a stricter signal tightens; the account's unrated rule and rules stay
	got := child.WithSignal(SignInSignal{Limit: Tier9, Unrated: UnratedHide})
	if got.MaxTier != Tier9 || got.Unrated != "tier_9" || got.SignalOnly || got.SignalLimit == nil || *got.SignalLimit != Tier9 || got.Version != 3 {
		t.Errorf("stricter signal: %+v", got)
	}
	if !got.AllowedBy(Subject{Kind: SubjectVideo, ID: "v-allowed"}, time.Now()) {
		t.Error("the guardian's allow rule was lost")
	}
	if child.MaxTier != Tier13 {
		t.Error("WithSignal changed the shared value")
	}
	// a looser signal does not loosen the account
	if got := child.WithSignal(SignInSignal{Limit: Tier18}); got.MaxTier != Tier13 {
		t.Errorf("looser signal: MaxTier %d", got.MaxTier)
	}
	// an account with no policy is held to the device alone
	open := &Effective{UserID: "v"}
	got = open.WithSignal(SignInSignal{Limit: Tier9, Unrated: "nonsense"})
	if !got.Restricted || !got.SignalOnly || got.MaxTier != Tier9 || got.Unrated != UnratedHide {
		t.Errorf("signal on an open account: %+v", got)
	}
	if got.TierPermits(&sixteen) || !got.TierPermits(&nine) || got.TierPermits(nil) {
		t.Error("signal on an open account: 16+ shown, 9+ hidden or unrated shown")
	}
	if open.Restricted {
		t.Error("WithSignal changed the shared value")
	}
	// a pending looser statement alone is only noted
	got = open.WithSignal(SignInSignal{Limit: NoLimit, Pending: true})
	if got.Restricted || !got.SignalPending {
		t.Errorf("pending alone: %+v", got)
	}
	// a nil policy is an open one
	var none *Effective
	if got := none.WithSignal(SignInSignal{Limit: NothingAllowed}); !got.Restricted || got.MaxTier != NothingAllowed {
		t.Errorf("nil policy: %+v", got)
	}
}
