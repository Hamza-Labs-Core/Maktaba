package billing

import (
	"strings"
	"testing"
	"time"
)

func TestMonthStart(t *testing.T) {
	in := time.Date(2026, 3, 15, 14, 22, 11, 0, time.UTC)
	want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if got := MonthStart(in); !got.Equal(want) {
		t.Errorf("MonthStart(%v) = %v, want %v", in, got, want)
	}
}

// Story 50.19 §1: Free has no relay, Basic and Pro do; a plan this build doesn't know keeps it
// (story 50.13: a home whose plan has no relay lists no relay connections).
func TestRelayIncluded(t *testing.T) {
	for p, want := range map[string]bool{PlanFree: false, PlanBasic: true, PlanPro: true, "a-plan-from-the-future": true} {
		if RelayIncluded(p) != want {
			t.Errorf("%s: relay %v, want %v", p, !want, want)
		}
	}
}

// Story 50.19 §2: the catalog's numbers.
func TestTheCatalog(t *testing.T) {
	b, p := Tiers[PlanBasic], Tiers[PlanPro]
	if b.RelayCapKbps != 3000 || b.RelaySlowCapKbps != 1500 || *b.MaxRelayStreams != 2 || b.FairUseBytesPerMo != 500*GB {
		t.Errorf("basic: %+v", b)
	}
	if p.RelayCapKbps != 0 || !p.PayAsYouGo || !p.ExtraStreamsAddOn || p.FairUseBytesPerMo != 500*GB {
		t.Errorf("pro: %+v", p)
	}
	if Tiers[PlanFree].Relay || *Tiers[PlanFree].PriceCents != 0 {
		t.Errorf("free: %+v", Tiers[PlanFree])
	}
	if len(Tiers) != 3 {
		t.Errorf("%d plans (family is gone)", len(Tiers))
	}
}

// Story 50.19 T01: the mode of every case of §3 and §4's order of spending.
func TestModeFor(t *testing.T) {
	allowance := 200 * GB
	old := Tiers
	defer func() { Tiers = old }()
	Tiers = map[string]Tier{}
	for k, v := range old {
		Tiers[k] = v
	}
	pro := Tiers[PlanPro]
	pro.AllowanceBytesPerMo = &allowance
	Tiers[PlanPro] = pro
	paygOn := PAYG{On: true, CapCents: 1000, PricePerGBCents: 10}
	for _, c := range []struct {
		name string
		plan string
		u    Usage
		p    PAYG
		mode string
		payg bool
		cap  int
	}{
		{"free", PlanFree, Usage{}, PAYG{}, ModeRefused, false, 0},
		{"basic under fair use", PlanBasic, Usage{Capped: 10 * GB}, PAYG{}, ModeCapped, false, 3000},
		{"basic over fair use", PlanBasic, Usage{Capped: 500 * GB}, PAYG{}, ModeCappedSlow, false, 1500},
		{"pro under its allowance", PlanPro, Usage{Full: 10 * GB}, PAYG{}, ModeFull, false, 0},
		{"pro past it, payg off", PlanPro, Usage{Full: allowance}, PAYG{}, ModeCapped, false, 3000},
		{"pro in payg", PlanPro, Usage{Full: allowance}, PAYG{On: true, CapCents: 1000, SpentCentsAlready: 999}, ModeFull, true, 0},
		{"pro past its payg cap", PlanPro, Usage{Full: allowance}, PAYG{On: true, CapCents: 1000, SpentCentsAlready: 1000}, ModeCapped, false, 3000},
		{"pro payg on without a cap", PlanPro, Usage{Full: allowance}, PAYG{On: true}, ModeCapped, false, 3000},
		{"pro with payg room left, capped bytes past fair use", PlanPro, Usage{Full: allowance, Capped: 500 * GB}, paygOn, ModeFull, true, 0},
		{"pro past payg and fair use", PlanPro, Usage{Full: allowance, Capped: 500 * GB}, PAYG{}, ModeCappedSlow, false, 1500},
		{"an unknown plan", "gold", Usage{Full: 1 << 50}, PAYG{}, ModeFull, false, 0},
	} {
		d := ModeFor(c.plan, c.u, c.p)
		if d.Mode != c.mode || d.PAYG != c.payg || d.CapKbps != c.cap {
			t.Errorf("%s: %+v, want %s payg=%v cap=%d", c.name, d, c.mode, c.payg, c.cap)
		}
	}
	// an allowance not set yet is not enforced
	Tiers[PlanPro] = old[PlanPro]
	if d := ModeFor(PlanPro, Usage{Full: 1 << 50}, PAYG{}); d.Mode != ModeFull {
		t.Errorf("a TBD allowance: %+v", d)
	}
}

func TestPAYGSpentCents(t *testing.T) {
	if got := PAYGSpentCents(3*GB, 25); got != 75 {
		t.Errorf("3 GB at 25¢: %d", got)
	}
	if PAYGSpentCents(0, 25) != 0 || PAYGSpentCents(GB, 0) != 0 {
		t.Error("nothing costs nothing")
	}
}

// Story 50.19 T12: the launch gate refuses paid plans while a value is TBD, naming each; with
// every value set it passes.
func TestLaunchCheck(t *testing.T) {
	err := LaunchCheck(Tiers)
	if err == nil {
		t.Fatal("today's catalog (prices TBD) passed")
	}
	for _, want := range []string{"basic.price", "pro.price", "pro.max_relay_streams", "pro.allowance", "pro.payg_price_per_gb", "pro.extra_stream_price"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v does not name %s", err, want)
		}
	}
	set := map[string]Tier{}
	for k, v := range Tiers {
		set[k] = v
	}
	n, a := 4, 200*GB
	b, p := set[PlanBasic], set[PlanPro]
	b.PriceCents = intp(500)
	p.PriceCents, p.MaxRelayStreams, p.AllowanceBytesPerMo = intp(1000), &n, &a
	p.PAYGPricePerGBCents, p.ExtraStreamPriceCents = intp(10), intp(200)
	set[PlanBasic], set[PlanPro] = b, p
	if err := LaunchCheck(set); err != nil {
		t.Fatalf("every value set: %v", err)
	}
}
