package router

// Epic 57 review, findings 8, 9, 21 and 22: the parental schemas say what the API
// answers, and the Arabic uses one term for one thing.

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// walkSchemas calls fn for every property named name anywhere in the document, with
// the JSON pointer of where it is.
func walkSchemas(v any, at, name string, fn func(at string, schema map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		if props, ok := x["properties"].(map[string]any); ok {
			if s, ok := props[name].(map[string]any); ok {
				fn(at+"/properties/"+name, s)
			}
		}
		for k, child := range x {
			walkSchemas(child, at+"/"+k, name, fn)
		}
	case []any:
		for _, child := range x {
			walkSchemas(child, at, name, fn)
		}
	}
}

func enumStrings(schema map[string]any) []string {
	var out []string
	for _, e := range schema["enum"].([]any) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func typeNames(schema map[string]any) []string {
	switch x := schema["type"].(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, e.(string))
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func openAPIJSON(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(sharedAPI(t, "openapi.json"), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// Finding 8: `unrated` is one of the six values the policy takes, wherever it is
// described; before, the schema allowed `hide` alone and a generated client refused a
// policy with `tier_9`.
func TestOpenAPIUnratedListsEveryValueThePolicyTakes(t *testing.T) {
	want := []string{"hide", "tier_0", "tier_13", "tier_16", "tier_18", "tier_9"}
	for _, v := range want {
		if !parental.ValidUnrated(v) {
			t.Fatalf("the policy does not take %q: the test's list is stale", v)
		}
	}
	seen := 0
	walkSchemas(openAPIJSON(t), "", "unrated", func(at string, s map[string]any) {
		if _, ok := s["enum"]; !ok {
			return
		}
		seen++
		if got := enumStrings(s); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: unrated is %v, want %v", at, got, want)
		}
		if d, _ := s["description"].(string); !strings.Contains(d, "`tier_18`") {
			t.Errorf("%s: the description does not say what a tier_N value does: %q", at, d)
		}
	})
	if seen < 6 {
		t.Fatalf("unrated is described with an enum in %d places, want at least 6", seen)
	}
}

// Finding 9: a tier's source is one the API writes. The document said `admin`, which
// nothing answers; the admin's own tier on a video is `admin_video`.
func TestOpenAPITierSourcesAreTheOnesTheAPIAnswers(t *testing.T) {
	answered := map[string]bool{}
	for _, s := range []parental.Source{parental.SourceAdminVideo, parental.SourceAdminContainer, parental.SourcePlex,
		parental.SourceGuide, parental.SourceYouTube, parental.SourcePlugin, parental.SourceDetected, parental.SourceLibraryDefault} {
		answered[string(s)] = true
	}
	doc := openAPIJSON(t)
	seen := 0
	walkSchemas(doc["paths"], "/paths", "source", func(at string, s map[string]any) {
		if _, ok := s["enum"]; !ok || (!strings.Contains(at, "age-tier") && !strings.Contains(at, "parental~1review") && !strings.Contains(at, "/parental/review")) {
			return
		}
		seen++
		has := false
		for _, v := range enumStrings(s) {
			if !answered[v] {
				t.Errorf("%s: source %q is never answered by the API", at, v)
			}
			has = has || v == string(parental.SourceAdminVideo)
		}
		if !has {
			t.Errorf("%s: the source enum has no admin_video", at)
		}
	})
	if seen < 8 {
		t.Fatalf("a tier's source is described in %d places, want at least 8", seen)
	}
	raw := string(sharedAPI(t, "openapi.json")) + string(sharedAPI(t, "openapi.yaml")) + string(sharedAPI(t, "openapi.ar.json"))
	if strings.Contains(raw, "`admin` when a tier was given") {
		t.Error("a description still names the source `admin`")
	}
}

// Finding 21: the device signal routes answer "tier_limit": null, so the schema's
// type takes null (as every other nullable field of the document says it).
func TestOpenAPIDeviceTierLimitMayBeNull(t *testing.T) {
	doc := openAPIJSON(t)
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/me/age-signal", "/users/{id}/parental/signals/{family}/clear"} {
		seen := 0
		walkSchemas(paths[p].(map[string]any)["post"].(map[string]any)["responses"], p, "tier_limit", func(at string, s map[string]any) {
			seen++
			if got := typeNames(s); !reflect.DeepEqual(got, []string{"integer", "null"}) {
				t.Errorf("%s: type %v, want integer or null", at, got)
			}
			if _, old := s["nullable"]; old {
				t.Errorf("%s: `nullable` is OpenAPI 3.0's word; this document is 3.1", at)
			}
		})
		if seen == 0 {
			t.Errorf("%s: no tier_limit in its answers", p)
		}
	}
}

// Finding 22: one Arabic term for one thing in the parental strings. The caller is
// المتصل (الطالب reads as "the student" in a document about children); to clear a
// device signal is مسح (إجازة, to authorise, fits only one of its two cases); an age
// tier is فئة عمرية, and تصنيف is kept for a rating label.
func TestOpenAPIArabicUsesOneTermForOneThing(t *testing.T) {
	var ar struct {
		Strings map[string]struct{ En, Ar string } `json:"strings"`
	}
	if err := json.Unmarshal(sharedAPI(t, "openapi.ar.json"), &ar); err != nil {
		t.Fatal(err)
	}
	caller := regexp.MustCompile(`(?i)\bcaller`)
	clears := regexp.MustCompile(`(?i)\bclear`)
	authorise := regexp.MustCompile(`[إأ]جاز|يجيز|تجيز`)
	tierAsRating := regexp.MustCompile(`التصنيف العمري|تصنيف عمري|التصنيفات العمرية|تصنيفها العمري|تصنيفًا عمري`)
	callers := 0
	for key, s := range ar.Strings {
		if strings.Contains(s.Ar, "الطالب") {
			t.Errorf("%s: الطالب for the caller: %s", key, s.Ar)
		}
		if caller.MatchString(s.En) && strings.Contains(s.Ar, "المتصل") {
			callers++
		}
		if clears.MatchString(s.En) && authorise.MatchString(s.Ar) {
			t.Errorf("%s: to clear is rendered as to authorise: %s", key, s.Ar)
		}
		if tierAsRating.MatchString(s.Ar) {
			t.Errorf("%s: an age tier is rendered as a rating: %s", key, s.Ar)
		}
		// the review routes spoke of the tier as تصنيف throughout
		if strings.Contains(key, "~1parental~1review") && strings.Contains(s.En, "tier") &&
			!strings.Contains(s.Ar, "فئ") {
			t.Errorf("%s: a tier with no فئة: %s", key, s.Ar)
		}
	}
	if callers < 20 {
		t.Fatalf("the caller is المتصل in %d strings, want at least 20", callers)
	}
	if s := ar.Strings["/paths/~1users~1{id}~1parental~1signals~1{family}~1clear/post/summary"]; !strings.Contains(s.Ar, "مسح") {
		t.Errorf("clearing a device signal is not مسح: %q", s.Ar)
	}
}
