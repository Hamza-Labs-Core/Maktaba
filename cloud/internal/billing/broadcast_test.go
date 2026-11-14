package billing

import (
	"strings"
	"testing"
	"time"
)

func withBroadcastPrices(t *testing.T) {
	t.Helper()
	old := Broadcaster
	Broadcaster.PriceCents = intp(5000)
	Broadcaster.PAYGPricePer100HCents = intp(600)
	Broadcaster.ExtraChannelPriceCents = intp(1000)
	Broadcaster.ExtraViewers100Cents = intp(1500)
	t.Cleanup(func() { Broadcaster = old })
}

// T02: the catalog: Free and Basic publish nothing, Pro has guest sharing, Pro with the add-on
// is Broadcaster; the add-on on any other plan grants nothing.
func TestBroadcastCatalog(t *testing.T) {
	cases := []struct {
		plan, bp, want string
	}{
		{PlanFree, "", ""},
		{PlanBasic, "", ""},
		{PlanPro, "", BroadcastGuest},
		{PlanPro, BroadcastPlanBroadcaster, BroadcastPlanBroadcaster},
		{PlanBasic, BroadcastPlanBroadcaster, ""},
		{PlanFree, BroadcastPlanBroadcaster, ""},
		{"gold", BroadcastPlanBroadcaster, ""},
	}
	for _, c := range cases {
		got := BroadcastFor(c.plan, c.bp)
		id := ""
		if got != nil {
			id = got.ID
		}
		if id != c.want {
			t.Errorf("%s + %q: %q, want %q", c.plan, c.bp, id, c.want)
		}
	}
	g, b := GuestSharing, Broadcaster
	if g.MaxChannels != 1 || !g.OnDemandOnly || g.MaxConcurrentViewers != 5 || g.ViewerHoursPerMonth != 50 ||
		g.AllowsAccess(AccessOpen) || !g.AllowsAccess(AccessInvite) || g.PAYG || g.FairUseMultiplier != 0 {
		t.Errorf("guest sharing: %+v", g)
	}
	if b.MaxChannels != 3 || b.OnDemandOnly || b.MaxConcurrentViewers != 100 || b.ViewerHoursPerMonth != 5000 ||
		b.MaxRungs != 3 || !b.AllowsAccess(AccessOpen) || !b.PAYG || b.FairUseMultiplier != 2.0 {
		t.Errorf("broadcaster: %+v", b)
	}
	// the prices are the owner's: TBD in the catalog
	if b.PriceCents != nil || b.PAYGPricePer100HCents != nil || b.ExtraChannelPriceCents != nil || b.ExtraViewers100Cents != nil {
		t.Errorf("a broadcast price is set in the catalog: %+v", b)
	}
	if l := b.LimitsWith(2, 1); l.MaxChannels != 5 || l.MaxConcurrentViewers != 200 {
		t.Errorf("broadcaster + 2 channels + 100 viewers: %+v", l)
	}
	if l := g.LimitsWith(2, 1); l.MaxChannels != 1 || l.MaxConcurrentViewers != 5 {
		t.Errorf("guest sharing takes no extras: %+v", l)
	}
}

// T07: the broadcasting gate refuses while any broadcast value is TBD, naming each.
func TestBroadcastLaunchCheck(t *testing.T) {
	err := BroadcastLaunchCheck(Tiers, Broadcaster)
	if err == nil {
		t.Fatal("the gate opened with every price TBD")
	}
	for _, name := range []string{"pro.price", "broadcaster.price", "broadcaster.payg_price_per_100h",
		"broadcaster.extra_channel_price", "broadcaster.extra_viewers_100_price"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%q not named: %v", name, err)
		}
	}
	withBroadcastPrices(t)
	tiers := map[string]Tier{}
	for k, v := range Tiers {
		tiers[k] = v
	}
	pro := tiers[PlanPro]
	pro.PriceCents = intp(1000)
	tiers[PlanPro] = pro
	if err := BroadcastLaunchCheck(tiers, Broadcaster); err != nil {
		t.Errorf("every value set: %v", err)
	}
	b := Broadcaster
	b.ExtraViewers100Cents = nil
	if err := BroadcastLaunchCheck(tiers, b); err == nil || !strings.Contains(err.Error(), "broadcaster.extra_viewers_100_price") ||
		strings.Contains(err.Error(), "broadcaster.price,") {
		t.Errorf("one TBD: %v", err)
	}
}

// T01: the mode for a new session.
func TestBroadcastModeFor(t *testing.T) {
	g, b := GuestSharing, Broadcaster
	h := func(hours int64) int64 { return hours * 60 }
	payg := BroadcastPAYG{On: true, CapCents: 2000, SpentCents: 500, Minutes: 5000, CapMinutes: 20000}
	spent := BroadcastPAYG{On: true, CapCents: 2000, SpentCents: 2000, Minutes: 20000, CapMinutes: 20000}
	cases := []struct {
		name string
		in   BroadcastInput
		want string
	}{
		{"guest within allowance", BroadcastInput{Tier: &g, Usage: BroadcastUsage{MinutesFull: h(49)}}, BroadcastFull},
		{"guest past allowance", BroadcastInput{Tier: &g, Usage: BroadcastUsage{MinutesFull: h(50)}}, BroadcastWait},
		{"guest with pay-as-you-go set (it has none)", BroadcastInput{Tier: &g, Usage: BroadcastUsage{MinutesFull: h(50)}, PAYG: payg}, BroadcastWait},
		{"guest at 5 viewers", BroadcastInput{Tier: &g, Concurrent: 5}, BroadcastWait},
		{"guest revoked", BroadcastInput{Tier: &g, Off: true}, BroadcastOff},
		{"broadcaster within allowance", BroadcastInput{Tier: &b, Usage: BroadcastUsage{MinutesFull: h(4999)}}, BroadcastFull},
		{"broadcaster in pay-as-you-go", BroadcastInput{Tier: &b, Usage: BroadcastUsage{MinutesFull: h(5000), MinutesPAYG: 5000}, PAYG: payg}, BroadcastFull},
		{"broadcaster at 5,000 h, pay-as-you-go off", BroadcastInput{Tier: &b, Usage: BroadcastUsage{MinutesFull: h(5000)}}, BroadcastLowest},
		{"broadcaster past the cap", BroadcastInput{Tier: &b, Usage: BroadcastUsage{MinutesFull: h(5000), MinutesPAYG: 20000}, PAYG: spent}, BroadcastLowest},
		{"broadcaster past 2x", BroadcastInput{Tier: &b, Usage: BroadcastUsage{MinutesFull: h(5000), MinutesLowest: h(5000)}}, BroadcastWait},
		{"bought hours don't eat fair use", BroadcastInput{Tier: &b, Usage: BroadcastUsage{MinutesFull: h(5000), MinutesLowest: h(4000), MinutesPAYG: h(3000)}, PAYG: spent}, BroadcastLowest},
		{"broadcaster at 100 viewers", BroadcastInput{Tier: &b, Concurrent: 100}, BroadcastWait},
		{"broadcaster with 100 more viewers bought", BroadcastInput{Tier: &b, Concurrent: 150, MaxConcurrent: 200}, BroadcastFull},
		{"broadcaster revoked", BroadcastInput{Tier: &b, Off: true}, BroadcastOff},
		{"no plan", BroadcastInput{}, BroadcastOff},
	}
	for _, c := range cases {
		c.in.Enforce = true
		if got := BroadcastModeFor(c.in); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// rollback: not enforced, full up to the concurrent-viewer limit; off stays off
	if got := BroadcastModeFor(BroadcastInput{Tier: &b, Usage: BroadcastUsage{MinutesFull: h(9000), MinutesLowest: h(9000)}}); got != BroadcastFull {
		t.Errorf("not enforced past everything: %s", got)
	}
	if got := BroadcastModeFor(BroadcastInput{Tier: &b, Concurrent: 100}); got != BroadcastWait {
		t.Errorf("not enforced at the viewer limit: %s", got)
	}
	if got := BroadcastModeFor(BroadcastInput{Tier: &b, Off: true}); got != BroadcastOff {
		t.Errorf("not enforced, off: %s", got)
	}
}

// §3: a window's full minutes go to the allowance, then pay-as-you-go up to the cap's worth,
// then full again (never billed).
func TestBroadcastSplit(t *testing.T) {
	b, g := Broadcaster, GuestSharing
	on := BroadcastPAYG{On: true, CapCents: 2000} // $20 at $6/100 h: 20,000 minutes
	full, payg := BroadcastSplit(&b, BroadcastUsage{MinutesFull: 299_990}, 100, on, 600)
	if full != 10 || payg != 90 {
		t.Errorf("across the allowance: %d full, %d payg", full, payg)
	}
	full, payg = BroadcastSplit(&b, BroadcastUsage{MinutesFull: 300_000, MinutesPAYG: 19_950}, 100, on, 600)
	if full != 50 || payg != 50 {
		t.Errorf("across the cap: %d full, %d payg", full, payg)
	}
	full, payg = BroadcastSplit(&b, BroadcastUsage{MinutesFull: 300_000}, 100, BroadcastPAYG{}, 600)
	if full != 100 || payg != 0 {
		t.Errorf("pay-as-you-go off: %d full, %d payg", full, payg)
	}
	full, payg = BroadcastSplit(&b, BroadcastUsage{MinutesFull: 300_000}, 100, on, 0)
	if full != 100 || payg != 0 {
		t.Errorf("no price: %d full, %d payg", full, payg)
	}
	full, payg = BroadcastSplit(&g, BroadcastUsage{MinutesFull: 3000}, 100, on, 600)
	if full != 100 || payg != 0 {
		t.Errorf("guest sharing: %d full, %d payg", full, payg)
	}
	if c := BroadcastPAYGCents(20_000, 600); c != 2000 {
		t.Errorf("20,000 minutes at $6/100 h: %d cents", c)
	}
	if m := BroadcastCapMinutes(1, 7); m != 857 {
		t.Errorf("cap minutes: %d", m)
	}
	// a cap whose cost rounds down is still reached in minutes
	p := BroadcastPAYG{On: true, CapCents: 1, SpentCents: BroadcastPAYGCents(857, 7), Minutes: 857, CapMinutes: 857}
	if p.HasRoom() {
		t.Error("a cap spent in minutes has room")
	}
}

func TestUsageWindowValid(t *testing.T) {
	id := "6f1c2b9e-0d4a-4b8e-9c3f-1a2b3c4d5e6f"
	at := time.Date(2026, 11, 13, 10, 5, 0, 0, time.UTC)
	if err := (UsageWindow{Publisher: id, Window: at, MinutesFull: 5}).Valid(); err != nil {
		t.Errorf("a good window: %v", err)
	}
	for name, w := range map[string]UsageWindow{
		"not on a boundary": {Publisher: id, Window: at.Add(time.Minute)},
		"negative":          {Publisher: id, Window: at, MinutesFull: -1},
		"huge":              {Publisher: id, Window: at, MinutesLowest: 100_001},
		"no time":           {Publisher: id},
		"bad publisher":     {Publisher: "home", Window: at},
	} {
		if w.Valid() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if k := WindowKey(id, at); k != id+":2026-11-13T10:05:00Z" {
		t.Errorf("key: %s", k)
	}
	if k := BroadcastReportID(id, at, at); k != id+":2026-11:2026-11-13T10" {
		t.Errorf("report id: %s", k)
	}
}

func TestBroadcastNotices(t *testing.T) {
	st := PublisherState{AllowanceMinutes: 3000, Usage: BroadcastUsage{MinutesFull: 2400}}
	if got := BroadcastNotices(st); len(got) != 1 || got[0] != NoticeAllowanceNear {
		t.Errorf("80 %%: %v", got)
	}
	st.Usage.MinutesFull = 3000
	st.PAYG = BroadcastPAYG{On: true, CapCents: 1000, SpentCents: 1000}
	if got := BroadcastNotices(st); strings.Join(got, ",") != "allowance_80,allowance_100,cap_80,cap_100" {
		t.Errorf("everything: %v", got)
	}
}
