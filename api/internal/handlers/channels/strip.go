package channels

// The channel strip (Story 27.11): a channel's own settings for the
// news-style bar the streaming engine burns into its picture.
//
//	PATCH  /api/channels/{id}/strip   partial object merged into mode_config.look.strip (admin);
//	                                  a null value drops that setting (the global one applies)
//	DELETE /api/channels/{id}/strip   removes look.strip: back to the rules tv_strip defaults (admin)
//
// Whatever a channel does not set follows the global rules section
// tv_strip (the Settings page). Like the rest of the look, the strip is not
// a user edit of the channel's definition: channels.edited_at is left
// alone, and the lineup plan writer keeps look.strip when it rewrites the
// channel (pipeline write_channels).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones on hosts without a zoneinfo database (Windows)
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// StripMaxClocks caps the strip's rotating clocks.
const StripMaxClocks = 8

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

type stripRange struct{ lo, hi float64 }

// stripNumbers are the numeric strip settings and their bounds; the int
// ones must be whole numbers.
var stripNumbers = map[string]stripRange{
	"height_pct":    {4, 25},
	"bg_opacity":    {0, 1},
	"font_size_pct": {15, 80},
	"scroll_speed":  {10, 600},
	"show_seconds":  {3, 600},
	"every_minutes": {1, 240},
	"clock_seconds": {2, 120},
}

var stripInts = map[string]bool{"scroll_speed": true, "show_seconds": true, "every_minutes": true, "clock_seconds": true}

var stripBools = map[string]bool{
	"enabled": true, "show_next": true, "show_clock": true, "show_channel_name": true,
}

var stripChoices = map[string][]string{
	"position":     {"bottom", "top"},
	"direction":    {"auto", "rtl", "ltr"},
	"overflow":     {"truncate", "scroll"},
	"show_mode":    {"always", "interval"},
	"clock_format": {"24h", "12h"},
	"digits":       {"auto", "arabic", "western"},
}

var stripColors = map[string]bool{"bg_color": true, "accent_color": true, "text_color": true}

// stripTexts: label settings and their rune limits.
var stripTexts = map[string]int{"next_label_ar": 40, "next_label_en": 40, "filler_label": 60}

// validateStrip checks a PATCH body and returns the cleaned values.
func validateStrip(in map[string]json.RawMessage) (map[string]any, []httperror.FieldError) {
	out := map[string]any{}
	var fe []httperror.FieldError
	bad := func(field, msg string) { fe = append(fe, httperror.FieldError{Field: field, Message: msg}) }
	names := make([]string, 0, len(in))
	for k := range in {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		raw := in[k]
		if string(raw) == "null" && k != "font_file" && knownStripKey(k) {
			out[k] = nil // back to the global setting
			continue
		}
		switch {
		case k == "font_file":
			bad(k, "the font is a host setting (Settings, Channel strip), not a channel's")
		case stripBools[k]:
			var b bool
			if json.Unmarshal(raw, &b) != nil {
				bad(k, "must be true or false")
			} else {
				out[k] = b
			}
		case stripChoices[k] != nil:
			var s string
			if json.Unmarshal(raw, &s) != nil || !oneOf(s, stripChoices[k]) {
				bad(k, "must be one of "+strings.Join(stripChoices[k], ", "))
			} else {
				out[k] = s
			}
		case stripColors[k]:
			var s string
			if json.Unmarshal(raw, &s) != nil || !hexColor.MatchString(s) {
				bad(k, "must be a colour #RRGGBB")
			} else {
				out[k] = strings.ToLower(s)
			}
		case stripTexts[k] > 0:
			var s string
			if json.Unmarshal(raw, &s) != nil {
				bad(k, "must be text")
			} else if s = strings.TrimSpace(s); utf8.RuneCountInString(s) > stripTexts[k] || hasControl(s) {
				bad(k, "at most "+strconv.Itoa(stripTexts[k])+" characters, no control characters")
			} else {
				out[k] = s
			}
		case k == "clocks":
			clocks, msg := validateClocks(raw)
			if msg != "" {
				bad(k, msg)
			} else {
				out[k] = clocks
			}
		default:
			rg, ok := stripNumbers[k]
			if !ok {
				bad(k, "unknown strip setting")
				continue
			}
			var f float64
			if json.Unmarshal(raw, &f) != nil || math.IsNaN(f) || f < rg.lo || f > rg.hi ||
				(stripInts[k] && f != math.Trunc(f)) {
				bad(k, "must be a number from "+fmtNum(rg.lo)+" to "+fmtNum(rg.hi))
			} else if stripInts[k] {
				out[k] = int(f)
			} else {
				out[k] = f
			}
		}
	}
	return out, fe
}

func knownStripKey(k string) bool {
	_, num := stripNumbers[k]
	return num || stripBools[k] || stripChoices[k] != nil || stripColors[k] || stripTexts[k] > 0 || k == "clocks"
}

func validateClocks(raw json.RawMessage) ([]map[string]any, string) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return nil, "must be a list of {zone, label_ar, label_en}"
	}
	if len(rows) > StripMaxClocks {
		return nil, "at most " + strconv.Itoa(StripMaxClocks) + " clocks"
	}
	out := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		c := map[string]any{}
		for k, v := range row {
			if k != "zone" && k != "label_ar" && k != "label_en" {
				return nil, "clock " + strconv.Itoa(i+1) + ": unknown field " + k
			}
			var s string
			if json.Unmarshal(v, &s) != nil {
				return nil, "clock " + strconv.Itoa(i+1) + ": " + k + " must be text"
			}
			s = strings.TrimSpace(s)
			if k == "zone" {
				if s == "" || strings.EqualFold(s, "local") {
					return nil, "clock " + strconv.Itoa(i+1) + ": a time zone such as Asia/Riyadh"
				}
				if _, err := time.LoadLocation(s); err != nil {
					return nil, "clock " + strconv.Itoa(i+1) + ": unknown time zone " + s
				}
			} else if utf8.RuneCountInString(s) > 30 || hasControl(s) {
				return nil, "clock " + strconv.Itoa(i+1) + ": " + k + " at most 30 characters"
			}
			c[k] = s
		}
		if _, ok := c["zone"]; !ok {
			return nil, "clock " + strconv.Itoa(i+1) + ": zone is required"
		}
		out = append(out, c)
	}
	return out, ""
}

func oneOf(s string, vals []string) bool {
	for _, v := range vals {
		if s == v {
			return true
		}
	}
	return false
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// stripOf is the channel's stored strip settings (nil when it has none).
func stripOf(c Channel) map[string]any {
	if s, ok := lookOf(c)["strip"].(map[string]any); ok {
		return s
	}
	return nil
}

func (h *Handler) loadStripChannel(w http.ResponseWriter, r *http.Request) (Channel, bool) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return Channel{}, false
	}
	id := chi.URLParam(r, "id")
	c, err := h.repo().get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return Channel{}, false
		}
		httperror.Write(w, r, httperror.Internal("load channel"))
		return Channel{}, false
	}
	return c, true
}

func (h *Handler) writeStrip(r *http.Request, c Channel, strip map[string]any) error {
	var v any
	if len(strip) > 0 {
		v = strip
	}
	mc, err := setLookKey(c.ModeConfig, "strip", v)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = h.DB.ExecContext(r.Context(), `UPDATE channels SET mode_config = $1, updated_at = $2 WHERE id = $3`,
		mc, now, c.ID)
	return err
}

// StripPatch serves PATCH /api/channels/{id}/strip.
func (h *Handler) StripPatch(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadStripChannel(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if e := common.ReadJSON(r, &body, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	vals, fe := validateStrip(body)
	if len(fe) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fe))
		return
	}
	merged := map[string]any{}
	for k, v := range stripOf(c) {
		merged[k] = v
	}
	for k, v := range vals {
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}
	if err := h.writeStrip(r, c, merged); err != nil {
		httperror.Write(w, r, httperror.Internal("save strip: "+err.Error()))
		return
	}
	h.writeChannel(w, r, c.ID)
}

// StripReset serves DELETE /api/channels/{id}/strip.
func (h *Handler) StripReset(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadStripChannel(w, r)
	if !ok {
		return
	}
	if err := h.writeStrip(r, c, nil); err != nil {
		httperror.Write(w, r, httperror.Internal("reset strip: "+err.Error()))
		return
	}
	h.writeChannel(w, r, c.ID)
}
