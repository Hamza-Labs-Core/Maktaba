package router

// Epic 57 story 57.13 T01: the API description keeps up with Epic 57's routes.
//
//   - TestOpenAPIDescribesEveryP57Route: every Epic 57 route the home mounts (P57Routes,
//     read from the walk of every mount) is an operation of shared/api/openapi.json, with
//     a summary, and none hides in the list of routes older than the description check.
//   - TestOpenAPIYAMLAgreesOnP57: openapi.yaml says the same as openapi.json for those
//     operations, for the paths Epic 57 changed, and for the two schemas it added. The two
//     files are kept by hand and had drifted; for these paths they may not again.
//   - TestOpenAPIP57HasArabic: every summary and description under those paths has its
//     Arabic in openapi.ar.json, written against the current English.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// p57Touched are the described paths Epic 57 changed that are not its own routes: what
// a restricted account is shown there, and the stream sessions under the path the
// router mounts them at.
var p57Touched = []string{
	"/search", "/search/suggest", "/tags", "/libraries/{id}/stats", "/channels", "/libraries/manage", "/capabilities",
	"/stream/sessions", "/stream/sessions/{id}", "/stream/sessions/{id}/progress", "/stream/sessions/{id}/errors", "/stream/capabilities",
}

func sharedAPI(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "api", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// p57SpecPaths are the description's paths that hold an Epic 57 route, plus p57Touched.
func p57SpecPaths(t *testing.T, paths map[string]any) []string {
	t.Helper()
	byKey := map[string]string{}
	for p := range paths {
		byKey[specParam.ReplaceAllString("/api"+p, "{}")] = p
	}
	set := map[string]bool{}
	for _, k := range P57Routes(t) {
		_, pattern, _ := strings.Cut(k, " ")
		if p, ok := byKey[specParam.ReplaceAllString(pattern, "{}")]; ok {
			set[p] = true
		}
	}
	for _, p := range p57Touched {
		if _, ok := paths[p]; !ok {
			t.Errorf("shared/api/openapi.json has no path %s", p)
		}
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func TestOpenAPIDescribesEveryP57Route(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(sharedAPI(t, "openapi.json"), &doc); err != nil {
		t.Fatal(err)
	}
	ops := map[string]json.RawMessage{}
	for p, item := range doc.Paths {
		for m, raw := range item {
			ops[specKey(m, "/api"+p)] = raw
		}
	}
	undocumented := readUndocumented(t)
	routes := P57Routes(t)
	for _, k := range routes {
		method, pattern, _ := strings.Cut(k, " ")
		if undocumented[k] {
			t.Errorf("%s is in %s: an Epic 57 route is described, never listed there", k, undocumentedFile)
		}
		raw, ok := ops[specKey(method, pattern)]
		if !ok {
			t.Errorf("shared/api/openapi.json does not describe %s", k)
			continue
		}
		var op struct {
			Summary   string                     `json:"summary"`
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(raw, &op); err != nil || strings.TrimSpace(op.Summary) == "" || len(op.Responses) < 2 {
			t.Errorf("%s: the description has no summary or names fewer than two answers (%v)", k, err)
		}
	}
	if len(routes) < 40 {
		t.Errorf("only %d Epic 57 routes were walked", len(routes))
	}
}

// jsonShape is v as encoding/json would have decoded it, so a YAML document compares
// with a JSON one (yaml.v3 gives ints, JSON float64).
func jsonShape(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOpenAPIYAMLAgreesOnP57(t *testing.T) {
	var j, y map[string]any
	if err := json.Unmarshal(sharedAPI(t, "openapi.json"), &j); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(sharedAPI(t, "openapi.yaml"), &y); err != nil {
		t.Fatal(err)
	}
	jp, _ := j["paths"].(map[string]any)
	yp, _ := jsonShape(t, y["paths"]).(map[string]any)
	paths := p57SpecPaths(t, jp)
	for _, p := range paths {
		if _, ok := yp[p]; !ok {
			t.Errorf("shared/api/openapi.yaml has no path %s", p)
			continue
		}
		if !reflect.DeepEqual(jp[p], yp[p]) {
			t.Errorf("%s: openapi.yaml and openapi.json say different things; change both", p)
		}
	}
	if len(paths) < 45 {
		t.Errorf("only %d paths were compared", len(paths))
	}
	schemas := func(doc map[string]any) map[string]any {
		c, _ := doc["components"].(map[string]any)
		s, _ := c["schemas"].(map[string]any)
		return s
	}
	js, ys := schemas(j), schemas(jsonShape(t, y).(map[string]any))
	for _, name := range []string{"ParentalSettings", "ParentalCapabilities"} {
		if js[name] == nil || !reflect.DeepEqual(js[name], ys[name]) {
			t.Errorf("components.schemas.%s: missing, or openapi.yaml and openapi.json differ", name)
		}
	}
	ji, _ := j["info"].(map[string]any)
	yi, _ := y["info"].(map[string]any)
	if ji["description"] != yi["description"] {
		t.Error("info.description differs between openapi.yaml and openapi.json")
	}
}

var arabicLetter = regexp.MustCompile(`[\x{0600}-\x{06FF}]`)

func TestOpenAPIP57HasArabic(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(sharedAPI(t, "openapi.json"), &doc); err != nil {
		t.Fatal(err)
	}
	var overlay struct {
		Strings map[string]struct{ En, Ar string } `json:"strings"`
	}
	if err := json.Unmarshal(sharedAPI(t, "openapi.ar.json"), &overlay); err != nil {
		t.Fatal(err)
	}
	part := strings.NewReplacer("~", "~0", "/", "~1")
	checked := 0
	var walk func(x any, ptr string)
	walk = func(x any, ptr string) {
		switch v := x.(type) {
		case []any:
			for i, e := range v {
				walk(e, ptr+"/"+strconv.Itoa(i))
			}
		case map[string]any:
			for k, e := range v {
				q := ptr + "/" + part.Replace(k)
				if s, ok := e.(string); ok && (k == "summary" || k == "description") && strings.TrimSpace(s) != "" {
					checked++
					got, ok := overlay.Strings[q]
					switch {
					case !ok:
						t.Errorf("shared/api/openapi.ar.json: %s has no Arabic", q)
					case got.En != s:
						t.Errorf("shared/api/openapi.ar.json: %s: the English changed; translate it again", q)
					case !arabicLetter.MatchString(got.Ar):
						t.Errorf("shared/api/openapi.ar.json: %s: the Arabic is empty or not Arabic", q)
					}
					continue
				}
				walk(e, q)
			}
		}
	}
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range p57SpecPaths(t, paths) {
		walk(paths[p], "/paths/"+part.Replace(p))
	}
	c, _ := doc["components"].(map[string]any)
	s, _ := c["schemas"].(map[string]any)
	for _, name := range []string{"ParentalSettings", "ParentalCapabilities"} {
		walk(s[name], "/components/schemas/"+name)
	}
	if checked < 200 {
		t.Errorf("only %d strings were checked", checked)
	}
}
