package broadcast

import (
	"slices"
	"testing"
	"time"
)

// a 1080p channel whose 3-rung ladder needs 15.6 Mbps of uplink (the story's numbers)
var src1080 = Source{VideoKbps: 9365, AudioKbps: 192, Height: 1080}

func passing() Inputs {
	return Inputs{
		Plan: Plan{Tier: "broadcaster", MaxChannels: 3, MaxRungs: 3, MaxTopKbps: 10000,
			Access: []string{"invite", "open"}, AgreementAccepted: true},
		Setup:              Setup{Access: "invite", Mode: "on_demand", Rungs: DefaultLadder},
		Source:             src1080,
		Settings:           DefaultSettings,
		PublishableSeconds: 80 * 24 * 36, // 80 % of 24 h
		UplinkKbps:         100_000,
		Host:               Host{MaxActive: 8, InUse: 1, Reserve: 2, Hardware: true, SessionCap: 8},
		RightsConfirmed:    true,
	}
}

func TestNeedIsTheLadderTimesOnePointOne(t *testing.T) {
	if got := NeedKbps(DefaultLadder, src1080); got != 15600 {
		t.Fatalf("need %d, want 15600", got)
	}
	if got := NeedKbps([]string{"720p", "480p"}, src1080); got != 5087 {
		t.Fatalf("720p + 480p need %d", got)
	}
}

// T01: each failing check in turn is the one named, in the story's order, and a later
// failure never hides an earlier one.
func TestPublishChecksNameTheFirstFailure(t *testing.T) {
	if r := Run(passing()); r != nil {
		t.Fatalf("all pass: %+v", r)
	}
	breaks := []struct {
		check, code string
		brk         func(*Inputs)
	}{
		{CheckPlan, CodeNeedsPlan, func(in *Inputs) { in.Plan.Tier = "" }},
		{CheckPlan, CodePlanChannels, func(in *Inputs) {
			in.Others = []Published{{}, {}, {}}
			in.UplinkKbps = 1 << 30
		}},
		{CheckPlan, CodePlanAccess, func(in *Inputs) { in.Plan.Access = []string{"invite"}; in.Setup.Access = "open" }},
		{CheckPlan, CodePlanOnDemand, func(in *Inputs) { in.Plan.OnDemandOnly = true; in.Setup.Mode = "always_on" }},
		{CheckPlan, CodePlanRungs, func(in *Inputs) { in.Plan.MaxRungs = 2 }},
		{CheckPlan, CodePlanTopKbps, func(in *Inputs) { in.Plan.MaxTopKbps = 3000 }},
		{CheckAgreement, CodeAgreement, func(in *Inputs) { in.Plan.AgreementAccepted = false }},
		{CheckPublishable, CodePublishable, func(in *Inputs) { in.PublishableSeconds = 5*3600 + 3599 }},
		{CheckUplink, CodeNoMeasurement, func(in *Inputs) { in.UplinkKbps = 0 }},
		{CheckUplink, CodeUplinkLow, func(in *Inputs) { in.UplinkKbps = 20_000 }},
		{CheckEncoder, CodeNoCapacity, func(in *Inputs) { in.Host.InUse = 5 }},
		{CheckEncoder, CodeSoftwareRungs, func(in *Inputs) { in.Host.Hardware = false }},
		{CheckEncoder, CodeSessions, func(in *Inputs) { in.Host.SessionsInUse = 7 }},
		{CheckRights, CodeRightsRequired, func(in *Inputs) { in.RightsConfirmed = false }},
	}
	for _, b := range breaks {
		in := passing()
		b.brk(&in)
		r := Run(in)
		if r == nil || r.Check != b.check || r.Code != b.code {
			t.Errorf("%s/%s: got %+v", b.check, b.code, r)
		}
	}
	// two failures: the earlier check is named
	in := passing()
	in.RightsConfirmed = false
	in.Plan.AgreementAccepted = false
	if r := Run(in); r == nil || r.Check != CheckAgreement {
		t.Fatalf("agreement before rights: %+v", r)
	}
	// exactly 6 hours is enough
	in = passing()
	in.PublishableSeconds = 6 * 3600
	if r := Run(in); r != nil {
		t.Fatalf("6 h publishable: %+v", r)
	}
}

// T04, acceptance: 20 Mbps measured, one channel published at 15.6 Mbps; a second at 15.6
// Mbps is refused with uplinkLow suggesting 720p + 480p.
func TestUplinkRefusesAndSuggests(t *testing.T) {
	in := passing()
	in.UplinkKbps = 20_000
	in.Others = []Published{{ChannelID: "a", Rungs: DefaultLadder, Source: src1080}}
	r := Run(in)
	if r == nil || r.Code != CodeUplinkLow {
		t.Fatalf("got %+v", r)
	}
	if r.Params["suggestion"] != "720p + 480p" || r.Params["kbps"] != 20_000 {
		t.Fatalf("params %+v", r.Params)
	}
	// with room left by the others, the largest ladder that fits in it comes first
	if got := Suggest(DefaultLadder, src1080, 25_000, 15_600); !slices.Equal(got, []string{"720p", "480p"}) {
		t.Fatalf("within the room: %v", got)
	}
	// nothing fits: no suggestion
	in.UplinkKbps = 2_000
	if r := Run(in); r == nil || r.Code != CodeUplinkLow || r.Params["suggestion"] != nil {
		t.Fatalf("nothing fits: %+v", r)
	}
	// a wider share lets it through: 0.8 × 40 Mbps = 32 Mbps ≥ 31.2
	in.UplinkKbps, in.Settings.UplinkShare = 40_000, 0.8
	if r := Run(in); r != nil {
		t.Fatalf("share 0.8: %+v", r)
	}
}

// T04: a later daily test below the need drops the newest channel's top rung first, and
// never stops a channel.
func TestUplinkDropRemovesTheNewestTopRung(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pub := []Published{
		{ChannelID: "old", Rungs: DefaultLadder, Source: src1080, PublishedAt: t0},
		{ChannelID: "new", Rungs: DefaultLadder, Source: src1080, PublishedAt: t0.Add(time.Hour)},
	}
	// 2 × 15.6 = 31.2 Mbps; a share of 0.5 of 50 Mbps is 25: the newest loses src
	drops := DropsFor(pub, 50_000, 0.5)
	if len(drops) != 1 || drops[0] != (Drop{ChannelID: "new", Dropped: 1}) {
		t.Fatalf("drops %+v", drops)
	}
	// a very low measurement: both go down to their last rung, and stay on
	drops = DropsFor(pub, 1_000, 0.5)
	want := []Drop{{ChannelID: "new", Dropped: 2}, {ChannelID: "old", Dropped: 2}}
	if !slices.Equal(drops, want) {
		t.Fatalf("drops %+v", drops)
	}
	for _, d := range drops {
		if got := Effective(DefaultLadder, d.Dropped); len(got) != 1 {
			t.Fatalf("%s stopped: %v", d.ChannelID, got)
		}
	}
	if drops := DropsFor(pub, 100_000, 0.5); len(drops) != 0 {
		t.Fatalf("enough uplink: %+v", drops)
	}
}

// T05, acceptance: max_active_channels 8, 5 in use, reserve 2; 3 rungs hold 1 + 0.5 + 0.5 =
// 2 slots, which would leave 1 free: noCapacity. The copy rung takes no encoder session.
func TestSlotsKeepTheReserve(t *testing.T) {
	if got := SlotsNeeded(DefaultLadder, 0.5); got != 2 {
		t.Fatalf("slots %v", got)
	}
	in := passing()
	in.Host = Host{MaxActive: 8, InUse: 5, Reserve: 2, Hardware: true}
	if r := Run(in); r == nil || r.Code != CodeNoCapacity || r.Params["reserve"] != 2 {
		t.Fatalf("got %+v", r)
	}
	in.Host.InUse = 4 // 8 - 4 - 2 = 2 left: exactly the reserve
	if r := Run(in); r != nil {
		t.Fatalf("at the reserve: %+v", r)
	}
	// src alone: one slot, no session (copy)
	if EncodedRungs([]string{RungSource}) != 0 || SlotsNeeded([]string{RungSource}, 0.5) != 1 {
		t.Fatal("the copy rung")
	}
	// sessions: 6 in use + 2 encoded rungs = 8 fits a cap of 8; 7 + 2 does not
	in.Host = Host{MaxActive: 16, Reserve: 2, Hardware: true, SessionCap: 8, SessionsInUse: 6}
	if r := Run(in); r != nil {
		t.Fatalf("6 + 2 sessions: %+v", r)
	}
	in.Host.SessionsInUse = 7
	if r := Run(in); r == nil || r.Code != CodeSessions {
		t.Fatalf("7 + 2 sessions: %+v", r)
	}
	// a copy-only ladder needs no session and no hardware
	in.Setup.Rungs = []string{RungSource}
	in.Host.Hardware = false
	if r := Run(in); r != nil {
		t.Fatalf("copy only on software: %+v", r)
	}
}

func TestLadderValidation(t *testing.T) {
	good := [][]string{{"src"}, {"src", "720p"}, DefaultLadder, {"src", "720p", "480p", "360p"}, {"720p", "480p"}}
	for _, l := range good {
		if err := ValidLadder(l, src1080); err != nil {
			t.Errorf("%v: %v", l, err)
		}
	}
	bad := [][]string{nil, {"480p", "720p"}, {"720p", "src"}, {"src", "src"}, {"src", "1440p"},
		{"src", "720p", "480p", "360p", "360p"}}
	for _, l := range bad {
		if ValidLadder(l, src1080) == nil {
			t.Errorf("%v accepted", l)
		}
	}
	// a 720p channel cannot carry a 720p rung under it
	if ValidLadder([]string{"src", "720p"}, Source{Height: 720}) == nil {
		t.Error("720p under a 720p channel")
	}
	if d := Describe(DefaultLadder, src1080); d != "1080p + 720p + 480p" {
		t.Errorf("describe %q", d)
	}
	if got := Effective(DefaultLadder, 9); !slices.Equal(got, []string{"480p"}) {
		t.Errorf("effective %v", got)
	}
}

func TestSettingsClamp(t *testing.T) {
	s := Settings{UplinkShare: 0.9, RungSlotWeight: 0}.Clamp()
	if s.UplinkShare != 0.5 || s.RungSlotWeight != 0.5 {
		t.Fatalf("%+v", s)
	}
	s = Settings{UplinkShare: 0.2, RungSlotWeight: 1, AllowSoftwareRungs: true}.Clamp()
	if s.UplinkShare != 0.2 || s.RungSlotWeight != 1 || !s.AllowSoftwareRungs {
		t.Fatalf("%+v", s)
	}
}
