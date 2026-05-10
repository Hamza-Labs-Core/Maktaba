package libraries

import (
	"encoding/json"
	"testing"
)

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
