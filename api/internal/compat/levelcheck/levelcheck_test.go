package levelcheck

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/compat"
)

const base = `
openapi: 3.1.0
paths:
  /things/{id}:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
        '404':
          $ref: '#/components/responses/NotFound'
    delete:
      responses:
        '204':
          description: gone
  /things:
    get:
      responses:
        '200':
          $ref: '#/components/responses/Things'
components:
  responses:
    NotFound:
      content:
        application/problem+json:
          schema:
            type: object
    Things:
      content:
        application/json:
          schema:
            type: object
            properties:
              items:
                type: array
                items:
                  $ref: '#/components/schemas/Thing'
  schemas:
    Base:
      type: object
      required: [id]
      properties:
        id:
          type: string
    Thing:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          required: [name]
          properties:
            name:
              type: string
            size:
              type: [integer, 'null']
            parent:
              $ref: '#/components/schemas/Thing'
`

func surface(t *testing.T, doc string) *Surface {
	t.Helper()
	s, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func edit(old, new string) string {
	if !strings.Contains(base, old) {
		panic("fixture: " + old)
	}
	return strings.Replace(base, old, new, 1)
}

func TestReadInlinesTheSurface(t *testing.T) {
	s := surface(t, base)
	thing := s.Operations["GET /things/{id}"]["200"]
	if thing == nil || thing.Type != "object" {
		t.Fatalf("GET /things/{id} 200 = %+v", thing)
	}
	for _, f := range []string{"id", "name", "size", "parent"} {
		if thing.Properties[f] == nil {
			t.Errorf("field %s missing (allOf not merged): %+v", f, thing.Properties)
		}
	}
	if thing.Properties["size"].Type != "integer" {
		t.Errorf("size type = %q, want integer (null dropped)", thing.Properties["size"].Type)
	}
	if thing.Properties["parent"].Ref == "" {
		t.Errorf("a recursive schema is named, not inlined forever")
	}
	if strings.Join(thing.Required, ",") != "id,name" {
		t.Errorf("required = %v", thing.Required)
	}
	if _, ok := s.Operations["GET /things/{id}"]["404"]; ok {
		t.Error("only success responses are the surface")
	}
	if b, ok := s.Operations["DELETE /things/{id}"]["204"]; !ok || b != nil {
		t.Errorf("a 204 with no body = %v, %v", b, ok)
	}
	if s.Operations["GET /things"]["200"].Properties["items"].Items.Properties["name"] == nil {
		t.Error("a components/responses ref is followed into its array items")
	}
}

// Additions are compatible: a new route, a new response, a new field.
func TestAdditionsAreCompatible(t *testing.T) {
	old := surface(t, base)
	cur := surface(t, edit(`            name:
              type: string`, `            name:
              type: string
            colour:
              type: string`)+`
  /more:
    get:
      responses:
        '200':
          description: ok
`)
	if d := Diff(old, cur); len(d) != 0 {
		t.Fatalf("Diff = %v, want none", d)
	}
}

func TestIncompatibleChanges(t *testing.T) {
	old := surface(t, base)
	cases := []struct {
		name, doc, want string
	}{
		{"removed field", edit(`            size:
              type: [integer, 'null']
`, ""), "GET /things/{id} 200.size: the field is gone"},
		{"removed field in array items", edit(`            size:
              type: [integer, 'null']
`, ""), "GET /things 200.items[].size: the field is gone"},
		{"type changed", edit(`            name:
              type: string`, `            name:
              type: integer`), "GET /things/{id} 200.name: the type changed from string to integer"},
		{"no longer required", edit(`          required: [name]
`, ""), "GET /things/{id} 200.name: the field is no longer always sent"},
		{"route gone", edit(`    delete:
      responses:
        '204':
          description: gone
`, ""), "DELETE /things/{id}: the route is gone"},
		{"response gone", edit(`        '204':
          description: gone`, `        '202':
          description: later`), "DELETE /things/{id} 204: the response is gone"},
	}
	for _, c := range cases {
		got := Diff(old, surface(t, c.doc))
		found := false
		for _, ch := range got {
			if ch.String() == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: Diff = %v, want %q", c.name, got, c.want)
		}
	}
}

// T02: a removed field fails the check, naming the field, until the level is raised (the
// new level documented, its snapshot written); then it passes.
func TestCheckFailsUntilTheLevelIsRaised(t *testing.T) {
	doc1 := "# API levels\n\n## Level 1\n\nThe first level.\n"
	snap1 := surface(t, base)
	snap1.Level = 1
	removed := surface(t, edit(`            size:
              type: [integer, 'null']
`, ""))

	problems := Check(removed, 1, snap1, doc1)
	if len(problems) != 1 || !strings.Contains(problems[0], "GET /things/{id} 200.size: the field is gone") ||
		!strings.Contains(problems[0], "Raise compat.Level to 2") {
		t.Fatalf("problems = %v, want the removed field named and the way out", problems)
	}

	// raising the level without saying why still fails
	snap2 := surface(t, edit(`            size:
              type: [integer, 'null']
`, ""))
	snap2.Level = 2
	problems = Check(removed, 2, snap2, doc1)
	if len(problems) != 1 || !strings.Contains(problems[0], `"## Level 2"`) {
		t.Fatalf("problems = %v, want the missing levels.md entry", problems)
	}
	// so does a level without its snapshot
	if problems = Check(removed, 2, nil, doc1+"\n## Level 2\n\n`size` is gone.\n"); len(problems) != 1 ||
		!strings.Contains(problems[0], "shared/api/levels/2.yaml is missing") {
		t.Fatalf("problems = %v, want the missing snapshot", problems)
	}
	// raised, documented and snapshotted: it passes
	if problems = Check(removed, 2, snap2, doc1+"\n## Level 2\n\n`size` is gone.\n"); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	s := surface(t, base)
	s.Level = 1
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.Level != 1 || len(Diff(s, back)) != 0 || len(Diff(back, s)) != 0 {
		t.Fatalf("round trip changed the surface")
	}
}

func repo(parts ...string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(append([]string{filepath.Dir(file), "..", "..", "..", ".."}, parts...)...)
}

// The real rule: shared/api/openapi.yaml against the snapshot of compat.Level, with
// docs/api/levels.md naming every level.
func TestOpenAPIKeepsItsLevel(t *testing.T) {
	doc, err := os.ReadFile(repo("shared", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := Read(doc)
	if err != nil {
		t.Fatal(err)
	}
	var snap *Surface
	if b, err := os.ReadFile(repo("shared", "api", "levels", itoa(compat.Level)+".yaml")); err == nil {
		if snap, err = Unmarshal(b); err != nil {
			t.Fatal(err)
		}
	}
	levels, err := os.ReadFile(repo("docs", "api", "levels.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range Check(cur, compat.Level, snap, string(levels)) {
		t.Error(p)
	}
	// every level from the oldest served up has its snapshot
	for n := compat.Oldest; n <= compat.Level; n++ {
		if _, err := os.Stat(repo("shared", "api", "levels", itoa(n)+".yaml")); err != nil {
			t.Errorf("level %d (served) has no snapshot: %v", n, err)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
