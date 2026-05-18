package libraries

import (
	"encoding/json"
	"strings"
	"testing"
)

// decode is a tiny helper: parse a JSON object literal into the
// map[string]any shape ValidateLibrarySettings expects (exactly what
// the PATCH handler feeds it after DeepMergeJSON).
func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad test JSON %q: %v", s, err)
	}
	return m
}

func TestValidateLibrarySettings_RejectsBadSTTBackend(t *testing.T) {
	errs, _ := ValidateLibrarySettings(decode(t, `{"stt":{"backend":"invalid"}}`))
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d: %+v", len(errs), errs)
	}
	if errs[0].Field != "settings/stt/backend" {
		t.Errorf("want offending path settings/stt/backend, got %q", errs[0].Field)
	}
}

func TestValidateLibrarySettings_AcceptsKnownSTTBackend(t *testing.T) {
	errs, _ := ValidateLibrarySettings(decode(t, `{"stt":{"backend":"whisper-mlx","model":"large-v3"}}`))
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %+v", errs)
	}
}

func TestValidateLibrarySettings_LanguageVocabulary(t *testing.T) {
	if errs, _ := ValidateLibrarySettings(decode(t, `{"language":"auto"}`)); len(errs) != 0 {
		t.Errorf("auto should be valid: %+v", errs)
	}
	if errs, _ := ValidateLibrarySettings(decode(t, `{"language":"ar"}`)); len(errs) != 0 {
		t.Errorf("ar should be valid: %+v", errs)
	}
	if errs, _ := ValidateLibrarySettings(decode(t, `{"language":"english"}`)); len(errs) != 1 {
		t.Errorf("english is not ISO-639-1, want 1 error, got %+v", errs)
	}
}

func TestValidateLibrarySettings_TypeMismatches(t *testing.T) {
	cases := []struct{ blob, path string }{
		{`{"multi_audio":"yes"}`, "settings/multi_audio"},
		{`{"sweep_interval_sec":-5}`, "settings/sweep_interval_sec"},
		{`{"sweep_interval_sec":1.5}`, "settings/sweep_interval_sec"},
		{`{"speaker_match_threshold":2}`, "settings/speaker_match_threshold"},
		{`{"topic_clusters":0}`, "settings/topic_clusters"},
		{`{"ignore_globs":["ok",3]}`, "settings/ignore_globs"},
		{`{"embedding":{"device":"tpu"}}`, "settings/embedding/device"},
	}
	for _, c := range cases {
		errs, _ := ValidateLibrarySettings(decode(t, c.blob))
		if len(errs) != 1 || errs[0].Field != c.path {
			t.Errorf("%s: want 1 error at %s, got %+v", c.blob, c.path, errs)
		}
	}
}

func TestValidateLibrarySettings_UnknownKeyIsWarningNotError(t *testing.T) {
	errs, warns := ValidateLibrarySettings(decode(t, `{"made_up_key":1,"stt":{"made_up":2}}`))
	if len(errs) != 0 {
		t.Errorf("unknown keys must not be errors: %+v", errs)
	}
	if len(warns) != 2 {
		t.Errorf("want 2 warnings (top-level + nested), got %v", warns)
	}
	joined := strings.Join(warns, "|")
	if !strings.Contains(joined, "made_up_key") || !strings.Contains(joined, "stt/made_up") {
		t.Errorf("warnings missing expected keys: %v", warns)
	}
}

func TestValidateLibrarySettings_EmptyIsValid(t *testing.T) {
	if errs, warns := ValidateLibrarySettings(map[string]any{}); len(errs) != 0 || len(warns) != 0 {
		t.Errorf("empty settings must be clean, got errs=%+v warns=%v", errs, warns)
	}
}

func TestValidateLibrarySettings_NestedMustBeObject(t *testing.T) {
	errs, _ := ValidateLibrarySettings(decode(t, `{"stt":"whisper-mlx"}`))
	if len(errs) != 1 || errs[0].Field != "settings/stt" {
		t.Errorf("stt-as-string should error at settings/stt, got %+v", errs)
	}
}
