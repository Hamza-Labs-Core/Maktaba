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
