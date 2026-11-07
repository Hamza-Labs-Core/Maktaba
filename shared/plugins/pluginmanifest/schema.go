package pluginmanifest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf8"
)

// node is one schema object of manifest.schema.json, in the subset the four validators
// read (the schema's description lists it). Anything else in the schema is an error at
// load, so a keyword one validator would skip can never be added silently.
type node struct {
	Type          []string         `json:"-"`
	RawType       json.RawMessage  `json:"type"`
	Properties    map[string]*node `json:"properties"`
	AdditionalOK  *bool            `json:"additionalProperties"`
	Required      []string         `json:"required"`
	Enum          []any            `json:"enum"`
	Pattern       string           `json:"pattern"`
	MinLength     *int             `json:"minLength"`
	MaxLength     *int             `json:"maxLength"`
	Minimum       *float64         `json:"minimum"`
	Maximum       *float64         `json:"maximum"`
	Items         *node            `json:"items"`
	MinItems      *int             `json:"minItems"`
	MaxItems      *int             `json:"maxItems"`
	UniqueItems   bool             `json:"uniqueItems"`
	Default       json.RawMessage  `json:"default"`
	Code          string           `json:"x-code"`
	TrustedOnly   bool             `json:"x-trusted-only"`
	UnknownCode   string           `json:"x-unknown-code"`
	re            *regexp.Regexp
	defaultValue  any
	hasDefault    bool
	propertyOrder []string
}

var allowedKeywords = map[string]bool{
	"$schema": true, "$id": true, "title": true, "description": true,
	"type": true, "properties": true, "additionalProperties": true, "required": true,
	"enum": true, "pattern": true, "minLength": true, "maxLength": true,
	"minimum": true, "maximum": true, "items": true, "minItems": true, "maxItems": true,
	"uniqueItems": true, "default": true, "x-code": true, "x-unknown-code": true, "x-trusted-only": true,
}

func parseSchema(raw []byte) (*node, error) {
	var n node
	if err := decodeNode(raw, &n); err != nil {
		return nil, err
	}
	return &n, nil
}

func decodeNode(raw []byte, n *node) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return err
	}
	for k := range keys {
		if !allowedKeywords[k] {
			return fmt.Errorf("pluginmanifest: schema keyword %q is not read by the validators", k)
		}
	}
	type plain node
	var p struct {
		plain
		Properties map[string]json.RawMessage `json:"properties"`
		Items      json.RawMessage            `json:"items"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	*n = node(p.plain)
	n.Properties = nil
	n.Items = nil
	if len(n.RawType) > 0 {
		var one string
		if json.Unmarshal(n.RawType, &one) == nil {
			n.Type = []string{one}
		} else if err := json.Unmarshal(n.RawType, &n.Type); err != nil {
			return err
		}
	}
	if n.Pattern != "" {
		re, err := regexp.Compile(n.Pattern)
		if err != nil {
			return err
		}
		n.re = re
	}
	if len(n.Default) > 0 {
		v, err := decodeJSONValue(n.Default)
		if err != nil {
			return err
		}
		n.defaultValue, n.hasDefault = v, true
	}
	if p.Properties != nil {
		n.Properties = map[string]*node{}
		for k, v := range p.Properties {
			c := &node{}
			if err := decodeNode(v, c); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			n.Properties[k] = c
			n.propertyOrder = append(n.propertyOrder, k)
		}
		sort.Strings(n.propertyOrder)
	}
	if len(p.Items) > 0 {
		c := &node{}
		if err := decodeNode(p.Items, c); err != nil {
			return fmt.Errorf("items: %w", err)
		}
		n.Items = c
	}
	return nil
}

// decodeJSONValue turns JSON into the manifest's own value types: map[string]any, []any,
// int64 for whole numbers, float64, string, bool.
func decodeJSONValue(raw []byte) (any, error) {
	var v any
	dec := json.NewDecoder(bytesReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return fromJSON(v), nil
}

func fromJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = fromJSON(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = fromJSON(x)
		}
		return t
	case json.Number:
		if i, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	}
	return v
}

func typeOf(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case int64:
		return "integer"
	case float64:
		return "number"
	case bool:
		return "boolean"
	}
	return "unknown"
}

func (n *node) code(parent string) string {
	if n.Code != "" {
		return n.Code
	}
	if parent != "" {
		return parent
	}
	return CodeInvalid
}

// check validates v against n and appends what fails. parentCode is the code of the
// nearest enclosing node with one. trusted is false for every manifest but Maktaba's own
// release-signed plugins: a key marked x-trusted-only is then plugin_trusted_field.
func (n *node) check(v any, path string, parentCode string, trusted bool, errs *[]Error) {
	code := n.code(parentCode)
	t := typeOf(v)
	if len(n.Type) > 0 {
		ok := false
		for _, want := range n.Type {
			if want == t || (want == "number" && t == "integer") {
				ok = true
			}
		}
		if !ok {
			*errs = append(*errs, Error{Code: code, Path: path, Detail: "type " + t})
			return
		}
	}
	if len(n.Enum) > 0 {
		ok := false
		for _, e := range n.Enum {
			if equalValue(fromJSON(e), v) {
				ok = true
			}
		}
		if !ok {
			*errs = append(*errs, Error{Code: code, Path: path, Detail: "not one of the allowed values"})
			return
		}
	}
	switch t {
	case "string":
		s := v.(string)
		count := utf8.RuneCountInString(s)
		if n.MinLength != nil && count < *n.MinLength || n.MaxLength != nil && count > *n.MaxLength {
			*errs = append(*errs, Error{Code: code, Path: path, Detail: "length"})
			return
		}
		if n.re != nil && !n.re.MatchString(s) {
			*errs = append(*errs, Error{Code: code, Path: path, Detail: "pattern"})
		}
	case "integer", "number":
		f := toFloat(v)
		if n.Minimum != nil && f < *n.Minimum || n.Maximum != nil && f > *n.Maximum {
			*errs = append(*errs, Error{Code: code, Path: path, Detail: "range"})
		}
	case "array":
		a := v.([]any)
		if n.MinItems != nil && len(a) < *n.MinItems || n.MaxItems != nil && len(a) > *n.MaxItems {
			*errs = append(*errs, Error{Code: code, Path: path, Detail: "items"})
			return
		}
		if n.UniqueItems {
			for i := range a {
				for j := 0; j < i; j++ {
					if equalValue(a[i], a[j]) {
						*errs = append(*errs, Error{Code: code, Path: path, Detail: "duplicate item"})
						return
					}
				}
			}
		}
		if n.Items != nil {
			for i, x := range a {
				n.Items.check(x, join(path, strconv.Itoa(i)), code, trusted, errs)
			}
		}
	case "object":
		o := v.(map[string]any)
		for _, r := range n.Required {
			if _, ok := o[r]; !ok {
				*errs = append(*errs, Error{Code: CodeMissingKey, Path: join(path, r)})
			}
		}
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			c, ok := n.Properties[k]
			if !ok {
				if n.AdditionalOK != nil && !*n.AdditionalOK {
					*errs = append(*errs, Error{Code: CodeUnknownKey, Path: join(path, k)})
				}
				continue
			}
			if c.TrustedOnly && !trusted {
				*errs = append(*errs, Error{Code: CodeTrustedField, Path: join(path, k)})
				continue
			}
			c.check(o[k], join(path, k), code, trusted, errs)
		}
	}
}

// fill sets every missing property that has a default, depth first, on a value that
// already passed check.
func (n *node) fill(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range n.propertyOrder {
			c := n.Properties[k]
			if _, ok := t[k]; !ok && c.hasDefault {
				t[k] = deepCopy(c.defaultValue)
			}
			if x, ok := t[k]; ok {
				t[k] = c.fill(x)
			}
		}
		return t
	case []any:
		if n.Items != nil {
			for i, x := range t {
				t[i] = n.Items.fill(x)
			}
		}
		return t
	}
	return v
}

func join(path, el string) string {
	if path == "" {
		return el
	}
	return path + "/" + el
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case float64:
		return t
	}
	return 0
}

func equalValue(a, b any) bool {
	if typeOf(a) == "integer" && typeOf(b) == "number" || typeOf(a) == "number" && typeOf(b) == "integer" {
		return toFloat(a) == toFloat(b)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		o := make(map[string]any, len(t))
		for k, x := range t {
			o[k] = deepCopy(x)
		}
		return o
	case []any:
		o := make([]any, len(t))
		for i, x := range t {
			o[i] = deepCopy(x)
		}
		return o
	}
	return v
}
