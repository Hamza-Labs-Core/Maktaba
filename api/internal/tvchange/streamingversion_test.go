package tvchange

import "testing"

// TV change #51 (2026-10-28): its release, built by the pipeline for a schedule change,
// records streaming_version "0.3.40"; the staging slot ran that very binary and reported
// "v0.3.40". The literal comparison never saw the slot healthy, and the change failed
// slot_not_healthy three times.
func TestSlotHealthyWhateverTheVersionSpelling(t *testing.T) {
	view := func(version string) *StagingView {
		return &StagingView{Active: true, Healthy: true, Slot: "amber", Plan: &StagingPlan{ChangeID: "c51"},
			Status: &SlotStatus{Role: "staging", Release: "3453bde4", Version: version}}
	}
	plan := func(version string) StagingPlan {
		return StagingPlan{ChangeID: "c51", ReleaseID: "3453bde4", Version: version}
	}
	for _, c := range []struct {
		name             string
		release, running string
		want             bool
	}{
		{"#51: the pipeline's spelling, the binary's report", "0.3.40", "v0.3.40", true},
		{"the binary's spelling, a bare report", "v0.3.40", "0.3.40", true},
		{"the same spelling", "v0.3.40", "v0.3.40", true},
		{"another binary", "0.3.40", "v0.3.39", false},
		{"a prerelease is not its release", "0.3.40", "v0.3.40-rc.1", false},
		{"no version recorded: any binary", "", "v0.3.40", true},
		// a pipeline run from a source checkout records its package's placeholder,
		// which names no binary (the slot runs git describe's version, or "unknown")
		{"a source checkout's release", "0.0.1", "v0.3.40-12-gabc1234", true},
		{"a source checkout's release, an unstamped slot", "0.0.1", "unknown", true},
		{"a slot that reports nothing", "0.3.40", "", false},
	} {
		if got := slotHealthy(view(c.running), plan(c.release)); got != c.want {
			t.Errorf("%s: release %q, slot %q: healthy %v, want %v", c.name, c.release, c.running, got, c.want)
		}
	}
}

func TestSameVersionSpellings(t *testing.T) {
	if !sameVersion("v0.3.40", " 0.3.40 ") || !sameVersion("V1.2.3", "1.2.3") || sameVersion("0.3.40", "0.3.41") {
		t.Fatal("sameVersion")
	}
}
