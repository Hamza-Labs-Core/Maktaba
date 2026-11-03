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
