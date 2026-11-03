package store

import (
	"testing"
	"time"
)

// Merge answers what component_desired_for answers: the override's want and hold when
// set, else the home's; the generations summed; an override with no want and no home
// row left out.
func TestMerge(t *testing.T) {
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	home := map[string]Desired{
		"transcription": {ID: "transcription", Want: "enabled", Generation: 3},
		"youtube":       {ID: "youtube", Want: "enabled", Hold: &Hold{By: "home", At: at}, Generation: 2},
		"llm":           {ID: "llm", Want: "enabled", Generation: 1},
	}
	ov := map[string]Override{
		"transcription": {ID: "transcription", Want: "installed", Generation: 2},
		"youtube":       {ID: "youtube", Generation: 4},
		"llm":           {ID: "llm", Hold: &Hold{By: "maya", At: at}, Generation: 1},
		"subtitles":     {ID: "subtitles", Want: "enabled", Generation: 1},
		"classify":      {ID: "classify", Generation: 5},
	}
	got := Merge(home, ov)
	if d := got["transcription"]; d.Want != "installed" || d.Generation != 5 {
		t.Errorf("transcription %+v", d)
	}
	if d := got["youtube"]; d.Want != "enabled" || d.Hold == nil || d.Hold.By != "home" || d.Generation != 6 {
		t.Errorf("youtube (held at home) %+v", d)
	}
	if d := got["llm"]; d.Hold == nil || d.Hold.By != "maya" {
		t.Errorf("llm (held on the machine) %+v", d)
	}
	if d := got["subtitles"]; d.Want != "enabled" || d.Generation != 1 {
		t.Errorf("subtitles (no home row) %+v", d)
	}
	if _, has := got["classify"]; has {
		t.Error("an override with no want and no home row is the catalog default")
	}
}
