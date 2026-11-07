// Package pluginsettings is a plugin's settings declared as data (Epic 54, story 54.11): the
// schema subset its ui/settings.schema.json may use, and the values an admin saves, checked
// by the home at install and on every save. Maktaba draws the form; no plugin code runs in
// the admin's browser.
package pluginsettings

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Codes of a refused schema or value (each has words, plugins.error.<code>).
const (
	CodeSchemaInvalid  = "settings_schema_invalid"
	CodeSecretForbid   = "settings_secret_forbidden"
	CodeInvalid        = "settings_invalid"
	MaxProperties      = 40
	MaxStringLength    = 2000
	maxPatternLength   = 200
	maxEnumValues      = 100
	forbiddenFieldWord = "token|password|api_key|apikey|secret"
)

// Widgets a property may name.
var Widgets = []string{"text", "textarea", "select", "slider", "switch", "library-picker", "language-picker"}

var (
	forbiddenName = regexp.MustCompile(`(?i)(` + forbiddenFieldWord + `)`)
	nameRe        = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	keyRe         = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	urlRe         = regexp.MustCompile(`(?i)\b(https?|ftp|wss?)://|\bwww\.`)
)

// Error is a refusal: its code, the property and why.
type Error struct {
	Code   string `json:"code"`
	Field  string `json:"field,omitempty"`
	Detail string `json:"detail"`
}

func (e *Error) Error() string {
	if e.Field != "" {
		return e.Code + ": " + e.Field + ": " + e.Detail
	}
	return e.Code + ": " + e.Detail
}

func fail(code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Detail: fmt.Sprintf(format, args...)}
}

// Property is one setting of a schema.
type Property struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Default   any      `json:"default"`
	Enum      []string `json:"enum,omitempty"`
	ItemsEnum []string `json:"items_enum,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	TitleKey  string   `json:"title_key"`
	HelpKey   string   `json:"help_key,omitempty"`
	Widget    string   `json:"widget,omitempty"`
}

// Schema is a checked settings schema, its properties in name order.
type Schema struct {
	Properties []Property `json:"properties"`
}

var propKeys = []string{"type", "default", "enum", "items", "minimum", "maximum", "maxLength", "pattern",
	"x-maktaba-title", "x-maktaba-help", "x-maktaba-widget", "description", "title"}

// Parse checks a ui/settings.schema.json against the subset (story 54.11 §1). strings are
// the plugin's English and Arabic words: every title and help key must be in both.
func Parse(raw []byte, strings map[string]map[string]string) (*Schema, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fail(CodeSchemaInvalid, "", "not JSON: %v", err)
	}
	for k := range doc {
		if k != "type" && k != "properties" && k != "$schema" && k != "title" && k != "description" {
			return nil, fail(CodeSchemaInvalid, "", "%q is not in the subset", k)
		}
	}
	if doc["type"] != "object" {
		return nil, fail(CodeSchemaInvalid, "", "the root is an object")
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return nil, fail(CodeSchemaInvalid, "", "no properties")
	}
	if len(props) > MaxProperties {
		return nil, fail(CodeSchemaInvalid, "", "%d properties, at most %d", len(props), MaxProperties)
	}
	s := &Schema{}
	for name, v := range props {
		if forbiddenName.MatchString(name) {
			return nil, fail(CodeSecretForbid, name, "a plugin with no internet has no secret to keep")
		}
		if !nameRe.MatchString(name) {
			return nil, fail(CodeSchemaInvalid, name, "a name is lowercase letters, digits and _")
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fail(CodeSchemaInvalid, name, "a property is an object")
		}
		p, err := property(name, m, strings)
		if err != nil {
			return nil, err
		}
		s.Properties = append(s.Properties, p)
	}
	sort.Slice(s.Properties, func(i, j int) bool { return s.Properties[i].Name < s.Properties[j].Name })
	return s, nil
}

func property(name string, m map[string]any, words map[string]map[string]string) (Property, error) {
	for k := range m {
		if !slices.Contains(propKeys, k) {
			if strings.HasPrefix(k, "x-") {
				return Property{}, fail(CodeSchemaInvalid, name, "%q: only x-maktaba-title, -help and -widget", k)
			}
			if k == "format" || k == "writeOnly" || k == "readOnly" {
				return Property{}, fail(CodeSecretForbid, name, "%q is not allowed", k)
			}
			return Property{}, fail(CodeSchemaInvalid, name, "%q is not in the subset", k)
		}
	}
	p := Property{Name: name}
	p.Type, _ = m["type"].(string)
	p.TitleKey, _ = m["x-maktaba-title"].(string)
	p.HelpKey, _ = m["x-maktaba-help"].(string)
	p.Widget, _ = m["x-maktaba-widget"].(string)
	if p.Widget != "" && !slices.Contains(Widgets, p.Widget) {
		return p, fail(CodeSchemaInvalid, name, "widget %q", p.Widget)
	}
	for _, key := range []string{p.TitleKey, p.HelpKey} {
		if key == "" {
			continue
		}
		if !keyRe.MatchString(key) {
			return p, fail(CodeSchemaInvalid, name, "a word key: %q", key)
		}
		for _, lang := range []string{"en", "ar"} {
			if words != nil && words[lang][key] == "" {
				return p, fail(CodeSchemaInvalid, name, "%s has no %s words", key, lang)
			}
		}
	}
	if p.TitleKey == "" {
		return p, fail(CodeSchemaInvalid, name, "x-maktaba-title is required")
	}
	num := func(k string) (*float64, error) {
		v, ok := m[k]
		if !ok {
			return nil, nil
		}
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fail(CodeSchemaInvalid, name, "%s is a number", k)
		}
		return &f, nil
	}
	var err error
	switch p.Type {
	case "string":
		if e, ok := m["enum"]; ok {
			if p.Enum, err = enumOf(name, e); err != nil {
				return p, err
			}
		}
		if ml, ok := m["maxLength"].(float64); ok {
			if ml < 1 || ml > MaxStringLength || ml != math.Trunc(ml) {
				return p, fail(CodeSchemaInvalid, name, "maxLength is 1 to %d", MaxStringLength)
			}
			p.MaxLength = int(ml)
		} else {
			p.MaxLength = MaxStringLength
		}
		if pat, ok := m["pattern"].(string); ok {
			if len(pat) > maxPatternLength || !safePattern(pat) {
				return p, fail(CodeSchemaInvalid, name, "the pattern is outside the safe subset")
			}
			if _, err := regexp.Compile(pat); err != nil {
				return p, fail(CodeSchemaInvalid, name, "the pattern: %v", err)
			}
			p.Pattern = pat
		}
	case "integer", "number":
		if p.Minimum, err = num("minimum"); err != nil {
			return p, err
		}
		if p.Maximum, err = num("maximum"); err != nil {
			return p, err
		}
		if p.Minimum != nil && p.Maximum != nil && *p.Minimum > *p.Maximum {
			return p, fail(CodeSchemaInvalid, name, "minimum above maximum")
		}
	case "boolean":
	case "array":
		items, ok := m["items"].(map[string]any)
		if !ok || items["type"] != "string" {
			return p, fail(CodeSchemaInvalid, name, "an array is of enum strings")
		}
		if p.ItemsEnum, err = enumOf(name, items["enum"]); err != nil {
			return p, err
		}
	default:
		return p, fail(CodeSchemaInvalid, name, "type %q", p.Type)
	}
	def, ok := m["default"]
	if !ok {
		return p, fail(CodeSchemaInvalid, name, "a default is required")
	}
	if err := p.check(def, nil); err != nil {
		return p, fail(CodeSchemaInvalid, name, "the default: %s", err.Detail)
	}
	p.Default = def
	return p, nil
}

func enumOf(name string, v any) ([]string, error) {
	l, ok := v.([]any)
	if !ok || len(l) == 0 || len(l) > maxEnumValues {
		return nil, fail(CodeSchemaInvalid, name, "enum is 1 to %d strings", maxEnumValues)
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		s, ok := x.(string)
		if !ok || s == "" || len(s) > 200 || urlRe.MatchString(s) {
			return nil, fail(CodeSchemaInvalid, name, "enum values are short strings with no URL")
		}
		out = append(out, s)
	}
	return out, nil
}

// check is one value against its property; libraries are the ids a library-picker may
// name (nil: not checked).
func (p Property) check(v any, libraries []string) *Error {
	bad := func(format string, args ...any) *Error { return fail(CodeInvalid, p.Name, format, args...) }
	switch p.Type {
	case "string":
		s, ok := v.(string)
		if !ok {
			return bad("a text")
		}
		if utf8.RuneCountInString(s) > p.MaxLength {
			return bad("at most %d characters", p.MaxLength)
		}
		if urlRe.MatchString(s) {
			return bad("no URLs")
		}
		if len(p.Enum) > 0 && !slices.Contains(p.Enum, s) {
			return bad("one of the choices")
		}
		if p.Pattern != "" && !regexp.MustCompile(p.Pattern).MatchString(s) {
			return bad("the form the plugin asks for")
		}
		if p.Widget == "library-picker" && libraries != nil && s != "" && !slices.Contains(libraries, s) {
			return bad("a library the plugin may see")
		}
	case "integer", "number":
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return bad("a number")
		}
		if p.Type == "integer" && f != math.Trunc(f) {
			return bad("a whole number")
		}
		if p.Minimum != nil && f < *p.Minimum {
			return bad("at least %v", *p.Minimum)
		}
		if p.Maximum != nil && f > *p.Maximum {
			return bad("at most %v", *p.Maximum)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return bad("on or off")
		}
	case "array":
		l, ok := v.([]any)
		if !ok || len(l) > maxEnumValues {
			return bad("a list of choices")
		}
		for _, x := range l {
			s, ok := x.(string)
			if !ok || !slices.Contains(p.ItemsEnum, s) {
				return bad("one of the choices")
			}
		}
	}
	return nil
}

// Defaults are every property's default.
func (s *Schema) Defaults() map[string]any {
	out := map[string]any{}
	for _, p := range s.Properties {
		out[p.Name] = p.Default
	}
	return out
}

// Check validates saved values (every property: a missing one takes its default; an
// unknown one is refused) and answers the full set.
func (s *Schema) Check(values map[string]any, libraries []string) (map[string]any, error) {
	out := s.Defaults()
	for k := range values {
		if !slices.ContainsFunc(s.Properties, func(p Property) bool { return p.Name == k }) {
			return nil, fail(CodeInvalid, k, "no such setting")
		}
	}
	for _, p := range s.Properties {
		v, ok := values[p.Name]
		if !ok {
			continue
		}
		if err := p.check(v, libraries); err != nil {
			return nil, err
		}
		out[p.Name] = v
	}
	return out, nil
}

// Carry is stored values brought to a new version's schema (story 54.11 §2): new fields
// take their defaults, removed ones are dropped, and one whose value no longer fits falls
// back to its default and is named in reset.
func (s *Schema) Carry(stored map[string]any) (values map[string]any, reset []string) {
	values = s.Defaults()
	for _, p := range s.Properties {
		v, ok := stored[p.Name]
		if !ok {
			continue
		}
		if p.check(v, nil) != nil {
			reset = append(reset, p.Name)
			continue
		}
		values[p.Name] = v
	}
	return values, reset
}

// safePattern: a pattern of the safe subset (story 54.11 §1): literals, character classes,
// \d \w \s, quantifiers and anchors; no groups, alternation, backreferences or lookaround.
func safePattern(p string) bool {
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '(', ')', '|':
			return false
		case '\\':
			if i+1 >= len(p) || !strings.ContainsRune("dwsDWS.-[]^$*+?{}", rune(p[i+1])) {
				return false
			}
			i++
		}
	}
	return true
}
