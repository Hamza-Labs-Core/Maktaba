package billing

import (
	"testing"
	"time"
)

func TestTiersDistinct(t *testing.T) {
	if Tiers[PlanFree].BandwidthBytesPerMo >= Tiers[PlanPro].BandwidthBytesPerMo {
		t.Errorf("Free should be tighter than Pro")
	}
	if Tiers[PlanPro].BandwidthBytesPerMo >= Tiers[PlanFamily].BandwidthBytesPerMo {
		t.Errorf("Pro should be tighter than Family")
	}
}

func TestMonthStart(t *testing.T) {
	in := time.Date(2026, 3, 15, 14, 22, 11, 0, time.UTC)
	want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if got := MonthStart(in); !got.Equal(want) {
		t.Errorf("MonthStart(%v) = %v, want %v", in, got, want)
	}
}

// Epic 50 story 50.13: whether a plan lists the relay follows its tier; today's tiers all
// include it (50.19 makes Free's relay "none"), and a plan this build doesn't know keeps it.
func TestRelayIncluded(t *testing.T) {
	for _, p := range []string{PlanFree, PlanPro, PlanFamily, "a-plan-from-the-future"} {
		if !RelayIncluded(p) {
			t.Errorf("%s: relay not included", p)
		}
	}
	old := Tiers
	defer func() { Tiers = old }()
	Tiers = map[string]Tier{PlanFree: {ID: PlanFree, RelayQoS: RelayNone}}
	if RelayIncluded(PlanFree) {
		t.Error("a tier whose relay is none still lists it")
	}
}
