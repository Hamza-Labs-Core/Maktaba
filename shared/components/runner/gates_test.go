package runner

import (
	"slices"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Gates is the runners' rule, exported for the API's capabilities (story 51.09): core
// and rowless components are on; installed is off, a hold stopped, an unmet requirement
// waiting with the group it misses; the kill switch turns everything on.
func TestGates(t *testing.T) {
	cat := components.Default()
	d := map[string]store.Desired{
		"core.api":      {ID: "core.api", Want: components.WantInstalled},
		"llm":           {ID: "llm", Want: components.WantInstalled, Generation: 3},
		"transcription": {ID: "transcription", Want: components.WantEnabled, Hold: &store.Hold{By: "maya", At: time.Now()}},
		"runtime.ml":    {ID: "runtime.ml", Want: components.WantInstalled},
		"subtitles":     {ID: "subtitles", Want: components.WantEnabled},
	}
	g := Gates(cat, d, false)
	if !g["core.api"].On || !g["youtube"].On {
		t.Fatal("core or rowless off")
	}
	if x := g["llm"]; x.On || x.Phase != components.PhaseOff || x.Generation != 3 {
		t.Fatalf("llm %+v", x)
	}
	if x := g["transcription"]; x.On || x.Phase != components.PhaseStopped || x.Reason != "held" {
		t.Fatalf("transcription %+v", x)
	}
	if x := g["subtitles"]; x.On || x.Reason != "dependency_off" || !slices.Contains(x.Missing, "transcription") {
		t.Fatalf("subtitles %+v", x)
	}
	for id, x := range Gates(cat, d, true) {
		if !x.On {
			t.Fatalf("%s off under the kill switch", id)
		}
	}
	if len(Gates(cat, nil, false)) != len(cat.Order()) {
		t.Fatal("not every component has a gate")
	}
}

// The licence is part of the shared rule (stories 51.09 + 51.19): the capabilities answer
// sees cloud.relay off exactly when the runners do, and back on inside its grace.
func TestGatesLicence(t *testing.T) {
	cat := components.Default()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	until := now.Add(time.Hour)
	d := map[string]store.Desired{
		"cloud.link":  {ID: "cloud.link", Want: components.WantEnabled},
		"cloud.relay": {ID: "cloud.relay", Want: components.WantEnabled, Licence: &store.Licence{Licensed: false}},
	}
	if x := GatesAt(cat, d, false, now)["cloud.relay"]; x.On || x.Reason != ReasonLicenceMissing || !x.Published {
		t.Fatalf("unlicensed relay %+v", x)
	}
	if !GatesAt(cat, d, false, now)["cloud.link"].On {
		t.Fatal("cloud.link gated")
	}
	d["cloud.relay"] = store.Desired{ID: "cloud.relay", Want: components.WantEnabled,
		Licence: &store.Licence{Licensed: true, Until: &until}}
	if x := GatesAt(cat, d, false, now)["cloud.relay"]; !x.On || !x.Until.Equal(until) {
		t.Fatalf("licensed relay %+v", x)
	}
	if x := GatesAt(cat, d, false, until.Add(time.Second))["cloud.relay"]; x.On || x.Reason != ReasonLicenceMissing {
		t.Fatalf("relay past its grace %+v", x)
	}
}
