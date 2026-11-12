package router

// Epic 57 story 57.03: the API description says where a restricted account is refused. Every
// described operation whose route the surface registry classes Refuse documents a 403: its
// own, or the shared `Restricted` answer (components.responses.Restricted, the 403 problem
// with `code: restricted`). A new refused route that is described without one fails here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

func TestOpenAPIRefusedRoutesDescribeTheRestrictedAnswer(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if d := doc.Components.Responses["Restricted"].Description; !strings.Contains(d, "`restricted`") || !strings.Contains(d, parental.TypeRestricted) {
		t.Errorf("components.responses.Restricted does not name the code and the problem type: %q", d)
	}
	mounted := map[string]walkedRoute{}
	for _, wr := range walkedRoutes(t) {
		mounted[specKey(wr.method, wr.pattern)] = wr
	}
	var missing []string
	refused := 0
	for path, item := range doc.Paths {
		for method, rawOp := range item {
			switch method {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			var op struct {
				Responses map[string]json.RawMessage `json:"responses"`
			}
			if err := json.Unmarshal(rawOp, &op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			wr, ok := mounted[specKey(method, "/api"+path)]
			if !ok {
				continue // not a route of this surface (TestOpenAPIDescribesEveryRoute's to say)
			}
			s, ok := parental.Surfaces.Lookup(wr.method, wr.pattern)
			if !ok || s.Class != parental.Refuse {
				continue
			}
			refused++
			if _, ok := op.Responses["403"]; !ok {
				missing = append(missing, strings.ToUpper(method)+" "+path)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d described operation(s) refuse a restricted account and document no 403. Add to each, in all three "+
			"files of shared/api:\n  '403':\n    $ref: '#/components/responses/Restricted'\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if refused < 50 {
		t.Errorf("only %d described operations are classed Refuse: the description or the registry was not read", refused)
	}
}
