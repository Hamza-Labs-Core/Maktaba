package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Schema is the decoded schema.json.
type Schema struct {
	Sections []*Section `json:"sections"`
}

// Section is one rules group stored under app_settings key rules.<Key>.
type Section struct {
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	What   string   `json:"what"`
	Fields []*Field `json:"fields"`

	byName map[string]*Field
}

// Field is one setting. raw keeps every schema key so GET passes
// through anything the generator adds without a Go change.
type Field struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Default     json.RawMessage `json:"default"`
	Minimum     *float64        `json:"minimum,omitempty"`
	Choices     []string        `json:"choices,omitempty"`
	Columns     []string        `json:"columns,omitempty"`
	ColumnKinds []string        `json:"column_kinds,omitempty"`
	// ColumnOptional marks the columns a row may leave out (they have a
	// default): a row saved before the column existed stays valid. Unlike a
	// "?" kind, a column present must still not be null.
	ColumnOptional []bool `json:"column_optional,omitempty"`
	TimeColumns    []int  `json:"time_columns,omitempty"`

	raw          map[string]json.RawMessage
	def          any
	rowsAsObject bool
}

// ParseSchema decodes and checks a schema document.
func ParseSchema(b []byte) (*Schema, error) {
	var s Schema
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	var rawDoc struct {
		Sections []struct {
			Fields []map[string]json.RawMessage `json:"fields"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(b, &rawDoc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, sec := range s.Sections {
		if sec.Key == "" || seen[sec.Key] {
			return nil, fmt.Errorf("section %d: missing or duplicate key %q", i, sec.Key)
		}
		seen[sec.Key] = true
		sec.byName = map[string]*Field{}
		for j, f := range sec.Fields {
			if f.Name == "" || sec.byName[f.Name] != nil {
				return nil, fmt.Errorf("%s field %d: missing or duplicate name %q", sec.Key, j, f.Name)
			}
			f.raw = rawDoc.Sections[i].Fields[j]
			def, err := decodeValue(f.Default)
			if err != nil {
				return nil, fmt.Errorf("%s.%s default: %w", sec.Key, f.Name, err)
			}
			f.def = def
			if f.Kind == "rows" {
				if len(f.Columns) == 0 {
					return nil, fmt.Errorf("%s.%s: rows field without columns", sec.Key, f.Name)
				}
				if len(f.ColumnKinds) != 0 && len(f.ColumnKinds) != len(f.Columns) {
					return nil, fmt.Errorf("%s.%s: column_kinds/columns length mismatch", sec.Key, f.Name)
				}
				if len(f.ColumnOptional) != 0 && len(f.ColumnOptional) != len(f.Columns) {
					return nil, fmt.Errorf("%s.%s: column_optional/columns length mismatch", sec.Key, f.Name)
				}
				f.rowsAsObject = true
				if rows, ok := def.([]any); ok && len(rows) > 0 {
					_, f.rowsAsObject = rows[0].(map[string]any)
				}
			}
			if err := f.Validate(def); err != nil {
				return nil, fmt.Errorf("%s.%s default fails its own kind: %w", sec.Key, f.Name, err)
			}
			sec.byName[f.Name] = f
		}
	}
	return &s, nil
}

func (s *Schema) section(key string) *Section {
	for _, sec := range s.Sections {
		if sec.Key == key {
			return sec
		}
	}
	return nil
}

func (s *Section) field(name string) *Field { return s.byName[name] }

// decodeValue parses JSON keeping numbers as json.Number so whole-number
// checks on ints are exact.
func decodeValue(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errors.New("not valid JSON")
	}
	if dec.More() {
		return nil, errors.New("not valid JSON")
	}
	return v, nil
}

// Validate checks v (decoded by decodeValue) against the field.
func (f *Field) Validate(v any) error {
	if f.Kind == "rows" {
		return f.validateRows(v)
	}
	return checkKind(f.Kind, v, f.Minimum, f.Choices)
}

var timeRe = regexp.MustCompile(`^([0-9]{2}):([0-9]{2})$`)

func checkKind(kind string, v any, minimum *float64, choices []string) error {
	if base, ok := strings.CutSuffix(kind, "?"); ok {
		if v == nil {
			return nil
		}
		kind = base
	}
	switch kind {
	case "", "any":
		return nil
	case "bool":
		if _, ok := v.(bool); !ok {
			return errors.New("must be true or false")
		}
	case "int":
		n, ok := v.(json.Number)
		if !ok {
			return errors.New("must be a whole number")
		}
		if _, err := strconv.ParseInt(n.String(), 10, 64); err != nil {
			f, ferr := n.Float64()
			if ferr != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
				return errors.New("must be a whole number")
			}
		}
		return checkMinimum(n, minimum)
	case "float":
		n, ok := v.(json.Number)
		if !ok {
			return errors.New("must be a number")
		}
		return checkMinimum(n, minimum)
	case "str":
		s, ok := v.(string)
		if !ok {
			return errors.New("must be a string")
		}
		if len(choices) > 0 {
			for _, c := range choices {
				if s == c {
					return nil
				}
			}
			return fmt.Errorf("must be one of %s", strings.Join(choices, ", "))
		}
	case "time":
		s, ok := v.(string)
		if !ok || !validTime(s) {
			return errors.New("must be a time HH:MM between 00:00 and 24:00")
		}
	case "list", "words":
		if !isStringList(v) {
			return errors.New("must be a list of strings")
		}
	case "map":
		m, ok := v.(map[string]any)
		if !ok {
			return errors.New("must be an object of string lists")
		}
		keys := sortedKeys(m)
		for _, k := range keys {
			if !isStringList(m[k]) {
				return fmt.Errorf("%s: must be a list of strings", k)
			}
		}
	default:
		return fmt.Errorf("unsupported kind %q", kind)
	}
	return nil
}

func checkMinimum(n json.Number, minimum *float64) error {
	f, err := n.Float64()
	if err != nil {
		return errors.New("must be a number")
	}
	if minimum != nil && f < *minimum {
		return fmt.Errorf("must be at least %s", strconv.FormatFloat(*minimum, 'g', -1, 64))
	}
	return nil
}

func validTime(s string) bool {
	m := timeRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	hh, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	if mm > 59 || hh > 24 {
		return false
	}
	return hh < 24 || mm == 0
}

func isStringList(v any) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range arr {
		if _, ok := x.(string); !ok {
			return false
		}
	}
	return true
}

func (f *Field) columnKind(i int) string {
	if len(f.ColumnKinds) > 0 {
		return f.ColumnKinds[i]
	}
	for _, t := range f.TimeColumns {
		if t == i {
			return "time"
		}
	}
	return "any"
}

func (f *Field) validateRows(v any) error {
	rows, ok := v.([]any)
	if !ok {
		return errors.New("must be a list of rows")
	}
	for ri, row := range rows {
		n := ri + 1
		if f.rowsAsObject {
			obj, ok := row.(map[string]any)
			if !ok {
				return fmt.Errorf("row %d: must be an object", n)
			}
			for _, k := range sortedKeys(obj) {
				if indexOf(f.Columns, k) < 0 {
					return fmt.Errorf("row %d: unknown column %q", n, k)
				}
			}
			for ci, col := range f.Columns {
				kind := f.columnKind(ci)
				cv, present := obj[col]
				if !present {
					if strings.HasSuffix(kind, "?") || (len(f.ColumnOptional) > ci && f.ColumnOptional[ci]) {
						continue
					}
					return fmt.Errorf("row %d: missing column %q", n, col)
				}
				if err := checkKind(kind, cv, nil, nil); err != nil {
					return fmt.Errorf("row %d %s: %w", n, col, err)
				}
			}
			continue
		}
		arr, ok := row.([]any)
		if !ok || len(arr) != len(f.Columns) {
			return fmt.Errorf("row %d: must be a list of %d values (%s)", n, len(f.Columns), strings.Join(f.Columns, ", "))
		}
		for ci, col := range f.Columns {
			if err := checkKind(f.columnKind(ci), arr[ci], nil, nil); err != nil {
				return fmt.Errorf("row %d %s: %w", n, col, err)
			}
		}
	}
	return nil
}

// equalValues reports whether a equals b for field f. Numbers compare
// by value (10 == 10.0); "words" compare as sets.
func equalValues(f *Field, a, b any) bool {
	if f.Kind == "words" && isStringList(a) && isStringList(b) {
		return reflect.DeepEqual(stringSet(a), stringSet(b))
	}
	return reflect.DeepEqual(canon(a), canon(b))
}

func stringSet(v any) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range v.([]any) {
		s := x.(string)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func canon(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return f
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canon(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = canon(e)
		}
		return out
	}
	return v
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}
