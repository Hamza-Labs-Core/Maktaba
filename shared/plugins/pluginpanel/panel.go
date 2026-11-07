// Package pluginpanel reads plugin panels (Epic 54, story 54.20): ui/panel.json, ui@1
// (shared/plugins/panel.schema.json), and the values a plugin writes for them. A panel is
// data, never code: each app draws its blocks natively, as plain text.
package pluginpanel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Block types (ui@1).
const (
	KV      = "kv"
	Badges  = "badges"
	Chips   = "chips"
	Table   = "table"
	Markers = "markers"
	Image   = "image"
	Text    = "text"
)

// Limits (54.20 §2).
const (
	MaxBlocks     = 10
	MaxKV         = 20
	MaxBadges     = 10
	MaxChips      = 20
	MaxColumns    = 6
	MaxRows       = 50
	MaxMarkers    = 200
	MaxText       = 1000
	MaxShort      = 200
	MaxImageBytes = 1 << 20
)

// CodeTextForbidden is the API's refusal of a URL, HTML, markdown or a control character
// in a value a panel shows.
const CodeTextForbidden = "plugin_text_forbidden"

// Block is one block of a panel.
type Block struct {
	Type    string   `json:"type"`
	Key     string   `json:"key,omitempty"`
	Label   string   `json:"label,omitempty"`
	Columns []string `json:"columns,omitempty"`
}

// Panel is a parsed ui/panel.json.
type Panel struct {
	UI     int     `json:"ui"`
	Title  string  `json:"title"`
	Blocks []Block `json:"blocks"`
}

// Error says what is wrong, with the code the API answers.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

func bad(format string, a ...any) *Error {
	return &Error{Code: "manifest_bad_panel", Detail: fmt.Sprintf(format, a...)}
}

var (
	wordKey = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	dataKey = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,256}$`)
)

// Parse reads ui/panel.json; unknown keys, block types and over-limit sizes are refused.
func Parse(raw []byte) (*Panel, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var p Panel
	if err := dec.Decode(&p); err != nil {
		return nil, bad("%v", err)
	}
	if p.UI != 1 {
		return nil, bad("ui is 1")
	}
	if !wordKey.MatchString(p.Title) {
		return nil, bad("title is a key of the plugin's strings")
	}
	if len(p.Blocks) == 0 || len(p.Blocks) > MaxBlocks {
		return nil, bad("1 to %d blocks", MaxBlocks)
	}
	keys := map[string]bool{}
	images := 0
	for i, b := range p.Blocks {
		switch b.Type {
		case KV, Badges, Chips, Table, Markers, Text:
			if !dataKey.MatchString(b.Key) {
				return nil, bad("block %d: a data key", i)
			}
			if keys[b.Key] {
				return nil, bad("block %d: key %q used twice", i, b.Key)
			}
			keys[b.Key] = true
		case Image:
			if b.Key != "" {
				return nil, bad("block %d: an image has no key (its artwork kind is panel)", i)
			}
			if images++; images > 1 {
				return nil, bad("one image per panel")
			}
		default:
			return nil, bad("block %d: no block type %q", i, b.Type)
		}
		if b.Label != "" && !wordKey.MatchString(b.Label) {
			return nil, bad("block %d: label is a key of the plugin's strings", i)
		}
		if b.Type == Table {
			if len(b.Columns) == 0 || len(b.Columns) > MaxColumns {
				return nil, bad("block %d: 1 to %d columns", i, MaxColumns)
			}
			for _, c := range b.Columns {
				if !wordKey.MatchString(c) {
					return nil, bad("block %d: a column is a key of the plugin's strings", i)
				}
			}
		} else if len(b.Columns) > 0 {
			return nil, bad("block %d: only a table has columns", i)
		}
	}
	return &p, nil
}

// Uses answers the block a data key feeds (nil: no block of this panel).
func (p *Panel) Uses(key string) *Block {
	for i := range p.Blocks {
		if p.Blocks[i].Key == key && p.Blocks[i].Type != Image {
			return &p.Blocks[i]
		}
	}
	return nil
}

var (
	urlRe  = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://|\bwww\.|\b(mailto|data|javascript|tel|file|blob):|\b[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z]{2,63}(/|:\d))`)
	htmlRe = regexp.MustCompile(`<[a-zA-Z!/?]|&(#\d+|#x[0-9a-fA-F]+|[a-zA-Z]{2,8});`)
	mdRe   = regexp.MustCompile(`\]\s*\(|!\[|\]\s*\[|^\s*\[[^\]]*\]:`)
)

// Forbidden answers why s may not be shown in a panel ("" when it may): a URL (any scheme,
// "www.", a host with a path or port), HTML, markdown links or images, or a control or bidi
// override character.
func Forbidden(s string) string {
	if !utf8.ValidString(s) {
		return "not UTF-8"
	}
	for _, r := range s {
		if unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F || r == 0xFEFF {
			return "a control or direction character"
		}
	}
	switch {
	case urlRe.MatchString(s):
		return "a URL"
	case htmlRe.MatchString(s):
		return "HTML"
	case mdRe.MatchString(s):
		return "markdown"
	}
	return ""
}

func refuse(code, format string, a ...any) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, a...)}
}

func text(v any, limit int, where string) *Error {
	s, ok := v.(string)
	if !ok {
		if _, num := v.(float64); num {
			return nil
		}
		return refuse("plugin_invalid", "%s: text", where)
	}
	if utf8.RuneCountInString(s) > limit {
		return refuse("plugin_too_large", "%s: at most %d characters", where, limit)
	}
	if why := Forbidden(s); why != "" {
		return refuse(CodeTextForbidden, "%s: %s", where, why)
	}
	return nil
}

// CheckValue checks a value (JSON text) a plugin writes for block b: its shape, its sizes,
// and that every text in it is plain.
func CheckValue(b *Block, raw []byte) *Error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return refuse("plugin_invalid", "JSON text")
	}
	list := func(max int) ([]any, *Error) {
		a, ok := v.([]any)
		if !ok {
			return nil, refuse("plugin_invalid", "%s: a list", b.Type)
		}
		if len(a) > max {
			return nil, refuse("plugin_too_large", "%s: at most %d", b.Type, max)
		}
		return a, nil
	}
	obj := func(x any, i int) (map[string]any, *Error) {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, refuse("plugin_invalid", "%s[%d]: an object", b.Type, i)
		}
		return m, nil
	}
	switch b.Type {
	case Text:
		return text(v, MaxText, "text")
	case Chips:
		a, e := list(MaxChips)
		if e != nil {
			return e
		}
		for i, x := range a {
			if e := text(x, MaxShort, fmt.Sprintf("chips[%d]", i)); e != nil {
				return e
			}
		}
	case KV:
		a, e := list(MaxKV)
		if e != nil {
			return e
		}
		for i, x := range a {
			m, e := obj(x, i)
			if e != nil {
				return e
			}
			for _, f := range []string{"label", "value"} {
				if e := text(m[f], MaxShort, fmt.Sprintf("kv[%d].%s", i, f)); e != nil {
					return e
				}
			}
		}
	case Badges:
		a, e := list(MaxBadges)
		if e != nil {
			return e
		}
		for i, x := range a {
			m, e := obj(x, i)
			if e != nil {
				return e
			}
			if e := text(m["text"], MaxShort, fmt.Sprintf("badges[%d].text", i)); e != nil {
				return e
			}
			switch m["tone"] {
			case nil, "neutral", "good", "warning":
			default:
				return refuse("plugin_invalid", "badges[%d].tone: neutral, good or warning", i)
			}
		}
	case Markers:
		a, e := list(MaxMarkers)
		if e != nil {
			return e
		}
		for i, x := range a {
			m, e := obj(x, i)
			if e != nil {
				return e
			}
			if t, ok := m["t_ms"].(float64); !ok || t < 0 || t != float64(int64(t)) {
				return refuse("plugin_invalid", "markers[%d].t_ms: a whole number of milliseconds", i)
			}
			if e := text(m["label"], MaxShort, fmt.Sprintf("markers[%d].label", i)); e != nil {
				return e
			}
		}
	case Table:
		a, e := list(MaxRows)
		if e != nil {
			return e
		}
		for i, x := range a {
			row, ok := x.([]any)
			if !ok || len(row) > len(b.Columns) {
				return refuse("plugin_invalid", "table[%d]: a row of at most %d cells", i, len(b.Columns))
			}
			for j, c := range row {
				if e := text(c, MaxShort, fmt.Sprintf("table[%d][%d]", i, j)); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
