package libraries

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLibraryResponse_WarningsOmittedWhenClean asserts the additive
// `warnings` field is `omitempty`: a valid config produces NO `warnings`
// key in the marshalled library response, so existing consumers and the
// List/Get reads (which never set it) are unaffected.
func TestLibraryResponse_WarningsOmittedWhenClean(t *testing.T) {
	out, err := json.Marshal(Library{ID: "x", Name: "n", Roots: []string{"/m"}, Settings: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "warnings") {
		t.Errorf("clean config must omit `warnings` key, got: %s", out)
	}
}

// TestLibraryResponse_WarningsRoundTripWhenSet confirms a populated
// Warnings slice marshals into the `warnings` array.
func TestLibraryResponse_WarningsRoundTripWhenSet(t *testing.T) {
	out, err := json.Marshal(Library{ID: "x", Name: "n", Warnings: []string{"unknown key foo"}})
	if err != nil {
		t.Fatal(err)
	}
	var back Library
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Warnings) != 1 || back.Warnings[0] != "unknown key foo" {
		t.Errorf("warnings lost in round-trip: %+v", back.Warnings)
	}
}

// TestCreateSettings_SurfacesUnknownKeyWarning reproduces the exact
// Create settings path (decode -> ValidateLibrarySettings -> assign to
// the Library response) and asserts a typo'd nested key (stt.bakend)
// and a bogus top-level key are NOT 422 (forward-compat round-trip) but
// ARE reported to the caller via the response `warnings` field.
//
// Fail-without-fix: before the fix the handler discarded `warnings`
// with `_` and Library had no Warnings field, so the marshalled
// response contained no `warnings` key even though the validator knew
// the keys were unknown — exactly the silent-accept footgun.
func TestCreateSettings_SurfacesUnknownKeyWarning(t *testing.T) {
	settings := json.RawMessage(`{"stt":{"bakend":"whisper-mlx"},"made_up_top":1}`)

	var decoded map[string]any
	if err := json.Unmarshal(settings, &decoded); err != nil {
		t.Fatal(err)
	}
	fieldErrs, warnings := ValidateLibrarySettings(decoded)
	if len(fieldErrs) != 0 {
		t.Fatalf("unknown keys must be forward-compat (no 422), got errs=%+v", fieldErrs)
	}
	resp := Library{ID: "x", Name: "n", Roots: []string{"/m"}, Settings: settings, Warnings: warnings}

	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var back Library
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(back.Warnings, "|")
	if !strings.Contains(joined, "stt/bakend") {
		t.Errorf("response must warn about typo'd nested key stt/bakend, got: %v", back.Warnings)
	}
	if !strings.Contains(joined, "made_up_top") {
		t.Errorf("response must warn about bogus top-level key, got: %v", back.Warnings)
	}
}

// TestPatchSettings_SurfacesUnknownKeyWarning reproduces the Patch path
// (deep-merge -> decode merged -> ValidateLibrarySettings -> assign to
// the Library response) and asserts an unknown key introduced by the
// PATCH is reported via the response `warnings` field, not silently
// swallowed, while remaining a forward-compat 200 (no 422).
func TestPatchSettings_SurfacesUnknownKeyWarning(t *testing.T) {
	existing := json.RawMessage(`{"stt":{"backend":"whisper-mlx"}}`)
	patch := json.RawMessage(`{"stt":{"bakend":"faster-whisper"}}`)

	merged, err := DeepMergeJSON(existing, patch)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(merged, &decoded); err != nil {
		t.Fatal(err)
	}
	fieldErrs, warnings := ValidateLibrarySettings(decoded)
	if len(fieldErrs) != 0 {
		t.Fatalf("unknown key must be forward-compat (no 422), got errs=%+v", fieldErrs)
	}
	cur := Library{ID: "x", Name: "n", Settings: merged, Warnings: warnings}

	out, err := json.Marshal(cur)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "stt/bakend") {
		t.Errorf("PATCH response must surface unknown-key warning, got: %s", out)
	}
}

func TestDeepMergeJSON_PreservesNestedKeys(t *testing.T) {
	a := json.RawMessage(`{"stt":{"backend":"whisper-mlx"}}`)
	b := json.RawMessage(`{"stt":{"model":"large-v3"}}`)
	out, err := DeepMergeJSON(a, b)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	stt := got["stt"].(map[string]any)
	if stt["backend"] != "whisper-mlx" {
		t.Errorf("lost backend: %#v", got)
	}
	if stt["model"] != "large-v3" {
		t.Errorf("missing model: %#v", got)
	}
}

func TestDeepMergeJSON_PatchKeyOverwritesScalar(t *testing.T) {
	a := json.RawMessage(`{"k":1}`)
	b := json.RawMessage(`{"k":2}`)
	out, _ := DeepMergeJSON(a, b)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["k"].(float64) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/mnt/media", "/mnt/media", true},
		{"/mnt/media", "/mnt/media/lectures", true},
		{"/mnt/media/lectures", "/mnt/media", true},
		{"/mnt/a", "/mnt/b", false},
		{"/mnt/media", "/mnt/media2", false},
	}
	for _, c := range cases {
		if got := pathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("pathsOverlap(%q,%q) = %v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestStringArray_ScanPostgresLiteral(t *testing.T) {
	var s stringArray
	if err := s.Scan(`{"a","b","c"}`); err != nil {
		t.Fatal(err)
	}
	if len(s) != 3 || s[0] != "a" || s[2] != "c" {
		t.Errorf("got %v", s)
	}
}

func TestStringArray_ScanJSON(t *testing.T) {
	var s stringArray
	if err := s.Scan([]byte(`["x","y"]`)); err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[1] != "y" {
		t.Errorf("got %v", s)
	}
}

func TestStringArray_RoundTrip(t *testing.T) {
	in := stringArray{"foo", "bar baz", `with "quote"`}
	v, err := in.Value()
	if err != nil {
		t.Fatal(err)
	}
	var out stringArray
	if err := out.Scan(v.(string)); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0] != "foo" || out[2] != `with "quote"` {
		t.Errorf("round-trip lost data: %v", out)
	}
}
