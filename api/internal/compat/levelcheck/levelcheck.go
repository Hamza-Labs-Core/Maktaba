// Package levelcheck keeps the API level honest (Epic 58 story 58.01): it reads the
// response surface of an OpenAPI document (every operation's success responses, their
// schemas inlined) and finds what an older app would get wrong in a newer one, a route or
// a response gone, a field gone, a field whose type changed, a field that may now be
// missing. Adding routes, responses or fields is compatible.
//
// shared/api/levels/<n>.yaml is the surface level n promised. The package's test diffs
// shared/api/openapi.yaml against the snapshot of compat.Level and fails on any
// incompatible change until the level is raised: compat.Level goes up by one,
// docs/api/levels.md gets a "## Level <n>" entry saying why, and the new level's snapshot is
// written (`go run ./cmd/levelcheck -write` from api/). The same command refreshes the
// current level's snapshot after a compatible change, so newly added fields are protected
// too.
package levelcheck

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Shape is a schema reduced to what a client reads: its type, its fields (and which are
// required), its items.
type Shape struct {
	Type       string            `yaml:"type,omitempty"`
	Properties map[string]*Shape `yaml:"properties,omitempty"`
	Required   []string          `yaml:"required,omitempty"`
	Items      *Shape            `yaml:"items,omitempty"`
	// Ref names a component met again inside itself (a recursive schema), not inlined twice.
	Ref string `yaml:"ref,omitempty"`
}

// Surface is the response surface of a document: "METHOD /path" → status → shape (nil when
// the response has no body).
type Surface struct {
	Level      int                          `yaml:"level"`
	Operations map[string]map[string]*Shape `yaml:"operations"`
}

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Read reduces an OpenAPI document (YAML or JSON) to its response surface.
func Read(doc []byte) (*Surface, error) {
	var root map[string]any
	if err := yaml.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("levelcheck: read the document: %w", err)
	}
	paths, _ := root["paths"].(map[string]any)
	if len(paths) == 0 {
		return nil, fmt.Errorf("levelcheck: the document has no paths")
	}
	rd := &reader{root: root}
	s := &Surface{Operations: map[string]map[string]*Shape{}}
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		for _, m := range methods {
			op, ok := ops[m].(map[string]any)
			if !ok {
				continue
			}
			key := strings.ToUpper(m) + " " + path
			out := map[string]*Shape{}
			responses, _ := op["responses"].(map[string]any)
			for status, resp := range responses {
				if !success(status) {
					continue
				}
				out[status] = rd.response(resp)
			}
			s.Operations[key] = out
		}
	}
	return s, nil
}

// success is a 2xx status (or the 2XX range).
func success(status string) bool {
	return len(status) == 3 && status[0] == '2'
}

type reader struct {
	root map[string]any
}

// resolve follows a local $ref ("#/components/schemas/X") and names it.
func (rd *reader) resolve(node any) (map[string]any, string) {
	m, _ := node.(map[string]any)
	ref, _ := m["$ref"].(string)
	if ref == "" {
		return m, ""
	}
	var cur any = rd.root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		cm, _ := cur.(map[string]any)
		cur = cm[part]
	}
	out, _ := cur.(map[string]any)
	return out, ref
}

func (rd *reader) response(node any) *Shape {
	resp, _ := rd.resolve(node)
	content, _ := resp["content"].(map[string]any)
	if len(content) == 0 {
		return nil
	}
	// the JSON body when there is one, else the first content type by name
	var media map[string]any
	if j, ok := content["application/json"].(map[string]any); ok {
		media = j
	} else {
		names := make([]string, 0, len(content))
		for k := range content {
			names = append(names, k)
		}
		sort.Strings(names)
		media, _ = content[names[0]].(map[string]any)
	}
	if media["schema"] == nil {
		return nil
	}
	return rd.shape(media["schema"], map[string]bool{})
}

// shape inlines a schema. seen holds the refs on the way here: a ref met again is named,
// not inlined (a recursive schema).
func (rd *reader) shape(node any, seen map[string]bool) *Shape {
	m, ref := rd.resolve(node)
	if ref != "" {
		if seen[ref] {
			return &Shape{Ref: ref}
		}
		seen[ref] = true
		defer delete(seen, ref)
	}
	s := &Shape{Type: schemaType(m["type"])}
	if all, ok := m["allOf"].([]any); ok {
		for _, part := range all {
			s.merge(rd.shape(part, seen))
		}
	}
	if props, ok := m["properties"].(map[string]any); ok {
		if s.Properties == nil {
			s.Properties = map[string]*Shape{}
		}
		for name, p := range props {
			s.Properties[name] = rd.shape(p, seen)
		}
	}
	if req, ok := m["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				s.Required = append(s.Required, name)
			}
		}
	}
	if items, ok := m["items"]; ok {
		s.Items = rd.shape(items, seen)
	}
	if s.Type == "" && len(s.Properties) > 0 {
		s.Type = "object"
	}
	sort.Strings(s.Required)
	s.Required = dedupe(s.Required)
	return s
}

// merge folds an allOf part into s.
func (s *Shape) merge(o *Shape) {
	if o == nil {
		return
	}
	if s.Type == "" {
		s.Type = o.Type
	}
	for k, v := range o.Properties {
		if s.Properties == nil {
			s.Properties = map[string]*Shape{}
		}
		s.Properties[k] = v
	}
	s.Required = append(s.Required, o.Required...)
	if s.Items == nil {
		s.Items = o.Items
	}
}

// schemaType is a schema's type; a 3.1 type list ([string, "null"]) is its non-null type.
func schemaType(t any) string {
	switch v := t.(type) {
	case string:
		return v
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok && s != "null" {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		return strings.Join(out, "|")
	}
	return ""
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// Change is one incompatible change: where, and what an older app would get wrong.
type Change struct {
	Where string
	What  string
}

func (c Change) String() string { return c.Where + ": " + c.What }

// Diff lists what an app built against old would get wrong in cur, sorted.
func Diff(old, cur *Surface) []Change {
	var out []Change
	for op, statuses := range old.Operations {
		now, ok := cur.Operations[op]
		if !ok {
			out = append(out, Change{op, "the route is gone"})
			continue
		}
		for status, shape := range statuses {
			ns, ok := now[status]
			if !ok {
				out = append(out, Change{op + " " + status, "the response is gone"})
				continue
			}
			out = append(out, diffShape(op+" "+status, shape, ns)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func diffShape(where string, old, cur *Shape) []Change {
	if old == nil {
		return nil
	}
	if cur == nil {
		return []Change{{where, "the body is gone"}}
	}
	if old.Ref != "" || cur.Ref != "" {
		return nil
	}
	var out []Change
	if old.Type != "" && cur.Type != "" && old.Type != cur.Type {
		out = append(out, Change{where, fmt.Sprintf("the type changed from %s to %s", old.Type, cur.Type)})
		return out
	}
	required := map[string]bool{}
	for _, r := range cur.Required {
		required[r] = true
	}
	for _, r := range old.Required {
		if !required[r] {
			if _, still := cur.Properties[r]; still {
				out = append(out, Change{where + "." + r, "the field is no longer always sent"})
			}
		}
	}
	for name, p := range old.Properties {
		np, ok := cur.Properties[name]
		if !ok {
			out = append(out, Change{where + "." + name, "the field is gone"})
			continue
		}
		out = append(out, diffShape(where+"."+name, p, np)...)
	}
	if old.Items != nil {
		out = append(out, diffShape(where+"[]", old.Items, cur.Items)...)
	}
	return out
}

// Marshal writes a surface as a snapshot.
func Marshal(s *Surface) ([]byte, error) {
	head := "# The API's response surface at this level (Epic 58 story 58.01): generated by\n" +
		"# `go run ./cmd/levelcheck -write` from api/; never edited by hand. docs/api/levels.md\n" +
		"# says what each level changed.\n"
	var buf bytes.Buffer
	buf.WriteString(head)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal reads a snapshot.
func Unmarshal(b []byte) (*Surface, error) {
	var s Surface
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Operations == nil {
		return nil, fmt.Errorf("levelcheck: the snapshot has no operations")
	}
	return &s, nil
}

var levelHeading = regexp.MustCompile(`(?m)^## Level (\d+)\b`)

// DocumentedLevels are the levels docs/api/levels.md has an entry for.
func DocumentedLevels(doc string) map[int]bool {
	out := map[int]bool{}
	for _, m := range levelHeading.FindAllStringSubmatch(doc, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out[n] = true
		}
	}
	return out
}

// Check is the level rule: cur must be compatible with the snapshot of level, that
// snapshot must exist and say its level, and docs/api/levels.md (doc) must have an entry
// for every level up to level. snapshot is nil when the file is missing.
func Check(cur *Surface, level int, snapshot *Surface, doc string) []string {
	var out []string
	documented := DocumentedLevels(doc)
	for n := 1; n <= level; n++ {
		if !documented[n] {
			out = append(out, fmt.Sprintf("docs/api/levels.md has no \"## Level %d\" entry: say what level %d changed", n, n))
		}
	}
	if snapshot == nil {
		return append(out, fmt.Sprintf("shared/api/levels/%d.yaml is missing: write it with `go run ./cmd/levelcheck -write` from api/", level))
	}
	if snapshot.Level != level {
		out = append(out, fmt.Sprintf("shared/api/levels/%d.yaml says level %d", level, snapshot.Level))
	}
	changes := Diff(snapshot, cur)
	if len(changes) > 0 {
		msg := fmt.Sprintf("shared/api/openapi.yaml changes the responses of API level %d in a way an older app would get wrong:", level)
		for _, c := range changes {
			msg += "\n  - " + c.String()
		}
		msg += fmt.Sprintf("\nRaise compat.Level to %d (api/internal/compat), say why under \"## Level %d\" in docs/api/levels.md, "+
			"and write shared/api/levels/%d.yaml (`go run ./cmd/levelcheck -write` from api/), or keep the old shape.", level+1, level+1, level+1)
		out = append(out, msg)
	}
	return out
}
