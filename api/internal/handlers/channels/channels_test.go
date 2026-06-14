package channels

import (
	"encoding/json"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Kids":               "kids",
		"  Action  Movies  ": "action-movies",
		"Sci-Fi & Fantasy":   "sci-fi-fantasy",
		"24/7 News!!!":       "24-7-news",
		"---":                "channel", // no usable chars
		"":                   "channel",
		"日本語":                "channel", // non-ASCII collapses away
		"Mix日本Channel":       "mix-channel",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugWithSuffix(t *testing.T) {
	if got := SlugWithSuffix("kids", 1); got != "kids" {
		t.Errorf("n=1 should be base, got %q", got)
	}
	if got := SlugWithSuffix("kids", 0); got != "kids" {
		t.Errorf("n=0 should be base, got %q", got)
	}
	if got := SlugWithSuffix("kids", 2); got != "kids-2" {
		t.Errorf("n=2 got %q", got)
	}
	if got := SlugWithSuffix("kids", 17); got != "kids-17" {
		t.Errorf("n=17 got %q", got)
	}
}

func TestValidateMode(t *testing.T) {
	for _, m := range []string{ModeShuffle, ModeMarathon, ModeSchedule, ModeSmartMix} {
		if err := ValidateMode(m); err != nil {
			t.Errorf("mode %q should be valid: %v", m, err)
		}
	}
	for _, m := range []string{"", "live", "Shuffle", "smartmix"} {
		if err := ValidateMode(m); err == nil {
			t.Errorf("mode %q should be invalid", m)
		}
	}
}

func TestValidateTransition(t *testing.T) {
	if err := ValidateTransition(""); err != nil {
		t.Errorf("empty transition allowed: %v", err)
	}
	if err := ValidateTransition("cut"); err != nil {
		t.Errorf("cut allowed: %v", err)
	}
	if err := ValidateTransition("crossfade"); err != nil {
		t.Errorf("crossfade allowed: %v", err)
	}
	if err := ValidateTransition("fade"); err == nil {
		t.Errorf("fade should be rejected")
	}
}

func TestValidateModeConfig_EmptyAlwaysValid(t *testing.T) {
	for _, m := range []string{ModeShuffle, ModeMarathon, ModeSchedule, ModeSmartMix} {
		for _, cfg := range []string{"", "{}", "null"} {
			if err := ValidateModeConfig(m, json.RawMessage(cfg)); err != nil {
				t.Errorf("mode=%s cfg=%q should be valid: %v", m, cfg, err)
			}
		}
	}
}

func TestValidateModeConfig_Shuffle(t *testing.T) {
	ok := []string{
		`{"reshuffle_period":"weekly"}`,
		`{"filter":{"genre":"comedy"}}`,
	}
	for _, c := range ok {
		if err := ValidateModeConfig(ModeShuffle, json.RawMessage(c)); err != nil {
			t.Errorf("shuffle %s should be valid: %v", c, err)
		}
	}
	bad := []string{
		`{"reshuffle_period":7}`,
		`{"filter":"comedy"}`,
		`[1,2,3]`,
	}
	for _, c := range bad {
		if err := ValidateModeConfig(ModeShuffle, json.RawMessage(c)); err == nil {
			t.Errorf("shuffle %s should be invalid", c)
		}
	}
}

func TestValidateModeConfig_Marathon(t *testing.T) {
	ok := []string{
		`{"series_id":"abc","order":"aired","loop":true}`,
		`{"source":{"library_id":"x"}}`,
		`{"series_id":"abc"}`,
	}
	for _, c := range ok {
		if err := ValidateModeConfig(ModeMarathon, json.RawMessage(c)); err != nil {
			t.Errorf("marathon %s should be valid: %v", c, err)
		}
	}
	bad := []string{
		`{}`,                                 // empty {} is treated as defaults — but marathon needs a source...
		`{"order":"aired"}`,                  // no series_id/source
		`{"series_id":123}`,                  // wrong type
		`{"series_id":"a","order":"chrono"}`, // bad order
		`{"series_id":"a","loop":"yes"}`,     // bad loop type
	}
	// Note: "{}" decodes to nil (defaults) and is accepted by the empty
	// rule; exclude it here and assert separately.
	for _, c := range bad[1:] {
		if err := ValidateModeConfig(ModeMarathon, json.RawMessage(c)); err == nil {
			t.Errorf("marathon %s should be invalid", c)
		}
	}
	if err := ValidateModeConfig(ModeMarathon, json.RawMessage(`{}`)); err != nil {
		t.Errorf("marathon {} is treated as defaults and allowed: %v", err)
	}
}

func TestValidateModeConfig_Schedule(t *testing.T) {
	ok := `{"slots":[{"start":"08:00","end":"12:00","days":["mon"]}]}`
	if err := ValidateModeConfig(ModeSchedule, json.RawMessage(ok)); err != nil {
		t.Errorf("schedule should be valid: %v", err)
	}
	bad := []string{
		`{"slots":[]}`,
		`{"slots":"morning"}`,
		`{"slots":[{"start":"08:00"}]}`, // missing end
		`{"slots":[{"end":"12:00"}]}`,   // missing start
		`{"slots":["x"]}`,               // non-object slot
	}
	for _, c := range bad {
		if err := ValidateModeConfig(ModeSchedule, json.RawMessage(c)); err == nil {
			t.Errorf("schedule %s should be invalid", c)
		}
	}
}

func TestValidateModeConfig_SmartMix(t *testing.T) {
	ok := []string{
		`{"daypart_profile":"family-network","diversity":0.5}`,
		`{"weights":{"comedy":2}}`,
		`{"diversity":0}`,
		`{"diversity":1}`,
	}
	for _, c := range ok {
		if err := ValidateModeConfig(ModeSmartMix, json.RawMessage(c)); err != nil {
			t.Errorf("smart_mix %s should be valid: %v", c, err)
		}
	}
	bad := []string{
		`{"diversity":2}`,
		`{"diversity":-0.1}`,
		`{"daypart_profile":5}`,
		`{"weights":[1,2]}`,
	}
	for _, c := range bad {
		if err := ValidateModeConfig(ModeSmartMix, json.RawMessage(c)); err == nil {
			t.Errorf("smart_mix %s should be invalid", c)
		}
	}
}

func TestValidateReorder(t *testing.T) {
	if err := validateReorder(nil); err == nil {
		t.Error("empty payload should be rejected")
	}
	if err := validateReorder([]ReorderEntry{{ID: "a", Number: 1}, {ID: "b", Number: 2}}); err != nil {
		t.Errorf("valid payload rejected: %v", err)
	}
	if err := validateReorder([]ReorderEntry{{ID: "a", Number: 1}, {ID: "a", Number: 2}}); err == nil {
		t.Error("duplicate id should be rejected")
	}
	if err := validateReorder([]ReorderEntry{{ID: "a", Number: 1}, {ID: "b", Number: 1}}); err == nil {
		t.Error("duplicate number should be rejected")
	}
	if err := validateReorder([]ReorderEntry{{ID: "", Number: 1}}); err == nil {
		t.Error("missing id should be rejected")
	}
	if err := validateReorder([]ReorderEntry{{ID: "a", Number: 0}}); err == nil {
		t.Error("non-positive number should be rejected")
	}
}

func TestTitleFromSnapshot(t *testing.T) {
	if got := titleFromSnapshot([]byte(`{"title":"The Movie"}`)); got != "The Movie" {
		t.Errorf("got %q", got)
	}
	if got := titleFromSnapshot([]byte(`{}`)); got != "" {
		t.Errorf("empty snapshot should yield empty title, got %q", got)
	}
	if got := titleFromSnapshot(nil); got != "" {
		t.Errorf("nil snapshot should yield empty title, got %q", got)
	}
	if got := titleFromSnapshot([]byte(`not json`)); got != "" {
		t.Errorf("bad json should yield empty title, got %q", got)
	}
}

func TestClamp01(t *testing.T) {
	cases := map[float64]float64{-1: 0, 0: 0, 0.5: 0.5, 1: 1, 2: 1}
	for in, want := range cases {
		if got := clamp01(in); got != want {
			t.Errorf("clamp01(%v) = %v want %v", in, got, want)
		}
	}
}

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 10: "10", 123: "123", -7: "-7"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q want %q", in, got, want)
		}
	}
}
