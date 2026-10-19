package channel

import (
	"encoding/json"
	"strings"
)

// Look is a channel's on-air presentation: `channels.logo_path` plus
// `channels.mode_config->'look'`:
//
//	{"watermark": {"path": str, "position": "top-right|top-left|bottom-right|bottom-left",
//	               "width_pct": number, "margin_pct": number},
//	 "loading_path": str|null,
//	 "loudnorm": {"enabled": bool, "target_lufs": number}}
//
// Missing keys fall back to defaults: no watermark, the logo as the
// slate card, loudness normalisation on at -16 LUFS.
type Look struct {
	Name        string
	LogoPath    string
	Watermark   *Watermark
	LoadingPath string
	Loudnorm    Loudnorm
	// Strip is the channel's own strip settings (mode_config.look.strip),
	// laid over rules tv_strip (ApplyStripRules).
	Strip json.RawMessage
}

// Watermark is the corner bug overlaid on program/filler/live blocks.
type Watermark struct {
	Path      string
	Position  string  // top-right | top-left | bottom-right | bottom-left
	WidthPct  float64 // of the output width
	MarginPct float64 // of the output height
}

// Loudnorm is the audio loudness setting.
type Loudnorm struct {
	Enabled    bool
	TargetLUFS float64
	// Filter is "loudnorm" (default, EBU R128) or "dynaudnorm".
	Filter string
}

// Defaults for the look contract.
const (
	DefaultTargetLUFS  = -16.0
	DefaultWMWidthPct  = 10.0
	DefaultWMMarginPct = 3.0
	DefaultWMPosition  = "top-right"
)

// DefaultLook is the look of a channel with no look config.
func DefaultLook() Look {
	return Look{Loudnorm: Loudnorm{Enabled: true, TargetLUFS: DefaultTargetLUFS, Filter: "loudnorm"}}
}

type lookJSON struct {
	Watermark *struct {
		Path      string   `json:"path"`
		Position  string   `json:"position"`
		WidthPct  *float64 `json:"width_pct"`
		MarginPct *float64 `json:"margin_pct"`
	} `json:"watermark"`
	LoadingPath *string `json:"loading_path"`
	Loudnorm    *struct {
		Enabled    *bool    `json:"enabled"`
		TargetLUFS *float64 `json:"target_lufs"`
		Filter     string   `json:"filter"`
	} `json:"loudnorm"`
	Strip json.RawMessage `json:"strip"`
}

// ParseLook decodes the mode_config->'look' blob (nil/garbage → defaults)
// and combines it with the channel's logo path.
func ParseLook(raw []byte, logoPath string) Look {
	l := DefaultLook()
	l.LogoPath = strings.TrimSpace(logoPath)
	if len(raw) == 0 {
		return l
	}
	var j lookJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return l
	}
	if w := j.Watermark; w != nil && strings.TrimSpace(w.Path) != "" {
		wm := &Watermark{Path: strings.TrimSpace(w.Path), Position: DefaultWMPosition,
			WidthPct: DefaultWMWidthPct, MarginPct: DefaultWMMarginPct}
		switch w.Position {
		case "top-right", "top-left", "bottom-right", "bottom-left":
			wm.Position = w.Position
		}
		if w.WidthPct != nil && *w.WidthPct > 0 && *w.WidthPct <= 100 {
			wm.WidthPct = *w.WidthPct
		}
		if w.MarginPct != nil && *w.MarginPct >= 0 && *w.MarginPct < 50 {
			wm.MarginPct = *w.MarginPct
		}
		l.Watermark = wm
	}
	if len(j.Strip) > 0 && string(j.Strip) != "null" {
		l.Strip = j.Strip
	}
	if j.LoadingPath != nil {
		l.LoadingPath = strings.TrimSpace(*j.LoadingPath)
	}
	if n := j.Loudnorm; n != nil {
		if n.Enabled != nil {
			l.Loudnorm.Enabled = *n.Enabled
		}
		if n.TargetLUFS != nil && *n.TargetLUFS < 0 && *n.TargetLUFS >= -70 {
			l.Loudnorm.TargetLUFS = *n.TargetLUFS
		}
		if n.Filter == "dynaudnorm" {
			l.Loudnorm.Filter = "dynaudnorm"
		}
	}
	return l
}
