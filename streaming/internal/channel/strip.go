package channel

// The channel strip (Story 27.11): a news-style lower third burned into the
// picture of every block — what is on now (the display title: series ·
// «الحلقة n» — title), what is next («التالي: …») and a clock rotating
// between cities (Makkah, Cairo, GMT, New York by default).
//
// Each block is its own ffmpeg, so the strip changes exactly at block
// boundaries, including the next block's pre-start a few seconds early: the
// block's own titles are written to per-block UTF-8 text files read by
// drawtext (textfile=, expansion=none: no title is ever put into the filter
// string, so nothing in a title needs escaping). Arabic needs shaping
// (text_shaping=1, libharfbuzz + libfribidi in the ffmpeg build); it is
// detected once per ffmpeg binary, and without it the strip is skipped (and
// logged once) rather than drawn with broken letters.
//
// The clock is a pair of drawtext instances (city label, time) whose text a
// sendcmd file replaces at every rotation step and every minute: the times
// are computed here with the IANA zones (time/tzdata is embedded, so
// Windows needs no zoneinfo), so daylight saving is exact even when it
// changes in the middle of a block, and the digits / 12-24 h format are free.
//
// Everything is CPU filtering on the decoded frames before the encoder
// (drawbox / drawtext are cheap); the hardware encoder path is unchanged.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // IANA zones on every host (Windows has no zoneinfo)
	"unicode"
	"unicode/utf8"
)

// StripClock is one city of the rotating clock.
type StripClock struct {
	Zone    string `json:"zone"`
	LabelAR string `json:"label_ar"`
	LabelEN string `json:"label_en"`
}

// StripSettings are the strip's settings: rules tv_strip (every channel's
// defaults) with the channel's own mode_config.look.strip over them.
type StripSettings struct {
	Enabled         bool
	Position        string // bottom | top
	HeightPct       float64
	BgColor         string
	BgOpacity       float64
	AccentColor     string  // the rule along the bar and the clock's box
	RuleHeight      float64 // the rule's thickness, percent of the bar (0: none)
	TextColor       string
	NextColor       string // the «التالي» line
	ClockTextColor  string // text on the accent colour (clock, channel name)
	IndicatorColor  string // the page number «١/٢»
	FontFile        string
	FontSizePct     float64
	Direction       string // auto | rtl | ltr
	ShowNext        bool
	NextLabelAR     string
	NextLabelEN     string
	FillerLabel     string
	Overflow        string // cycle | truncate | scroll
	PageSeconds     int    // cycle: seconds a page shows (scaled by its length)
	PageIndicator   bool   // cycle: «١/٢» beside a paged line
	ScrollSpeed     int
	ShowMode        string // always | interval
	ShowSeconds     int
	EveryMinutes    int
	ShowClock       bool
	Clocks          []StripClock
	ClockSeconds    int
	ClockFormat     string // 24h | 12h
	Digits          string // auto | arabic | western
	ShowChannelName bool
}

// DefaultStripSettings mirrors the pipeline's TvStripConfig defaults.
func DefaultStripSettings() StripSettings {
	return StripSettings{
		Enabled: false, Position: "bottom", HeightPct: 9, BgColor: "#0b5d3b", BgOpacity: 0.9,
		AccentColor: "#c9a227", RuleHeight: 4, TextColor: "#ffffff", NextColor: "#e8d9a8",
		ClockTextColor: "#0b3d27", IndicatorColor: "#eac85e", FontSizePct: 42, Direction: "auto",
		ShowNext: true, NextLabelAR: "التالي", NextLabelEN: "Next", Overflow: "cycle",
		PageSeconds: 4, PageIndicator: true,
		ScrollSpeed: 90, ShowMode: "always", ShowSeconds: 20, EveryMinutes: 10,
		ShowClock: true, Clocks: DefaultStripClocks(), ClockSeconds: 8, ClockFormat: "24h",
		Digits: "auto",
	}
}

// DefaultStripClocks: Makkah, Cairo, Greenwich, New York.
func DefaultStripClocks() []StripClock {
	return []StripClock{
		{Zone: "Asia/Riyadh", LabelAR: "مكة المكرمة", LabelEN: "Makkah"},
		{Zone: "Africa/Cairo", LabelAR: "القاهرة", LabelEN: "Cairo"},
		{Zone: "UTC", LabelAR: "غرينتش", LabelEN: "GMT"},
		{Zone: "America/New_York", LabelAR: "نيويورك", LabelEN: "New York"},
	}
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ApplyStripRules overlays stored strip settings (rules.tv_strip, or a
// channel's look.strip with channel=true) on base. A value that cannot be
// used is skipped and named in bad. A channel may not name a font file (a
// path on the streaming host): that is a host setting.
func ApplyStripRules(base StripSettings, raw []byte, channel bool) (out StripSettings, bad []string) {
	out = base
	out.Clocks = append([]StripClock(nil), base.Clocks...)
	for name, v := range decodeRules(raw) {
		var ok bool
		switch name {
		case "enabled":
			ok = setBool(&out.Enabled, v)
		case "show_next":
			ok = setBool(&out.ShowNext, v)
		case "show_clock":
			ok = setBool(&out.ShowClock, v)
		case "show_channel_name":
			ok = setBool(&out.ShowChannelName, v)
		case "position":
			ok = setChoice(&out.Position, v, "bottom", "top")
		case "direction":
			ok = setChoice(&out.Direction, v, "auto", "rtl", "ltr")
		case "overflow":
			ok = setChoice(&out.Overflow, v, "cycle", "truncate", "scroll")
		case "page_seconds":
			ok = setInt(&out.PageSeconds, v, 2, 30)
		case "page_indicator":
			ok = setBool(&out.PageIndicator, v)
		case "show_mode":
			ok = setChoice(&out.ShowMode, v, "always", "interval")
		case "clock_format":
			ok = setChoice(&out.ClockFormat, v, "24h", "12h")
		case "digits":
			ok = setChoice(&out.Digits, v, "auto", "arabic", "western")
		case "height_pct":
			ok = setFloat(&out.HeightPct, v, 4, 25)
		case "bg_opacity":
			ok = setFloat(&out.BgOpacity, v, 0, 1)
		case "font_size_pct":
			ok = setFloat(&out.FontSizePct, v, 15, 80)
		case "scroll_speed":
			ok = setInt(&out.ScrollSpeed, v, 10, 600)
		case "show_seconds":
			ok = setInt(&out.ShowSeconds, v, 3, 600)
		case "every_minutes":
			ok = setInt(&out.EveryMinutes, v, 1, 240)
		case "clock_seconds":
			ok = setInt(&out.ClockSeconds, v, 2, 120)
		case "bg_color", "accent_color", "text_color", "next_color", "clock_text_color", "indicator_color":
			s, good := jsonString(v)
			if ok = good && hexColor.MatchString(s); ok {
				*map[string]*string{
					"bg_color": &out.BgColor, "accent_color": &out.AccentColor,
					"text_color": &out.TextColor, "next_color": &out.NextColor,
					"clock_text_color": &out.ClockTextColor, "indicator_color": &out.IndicatorColor,
				}[name] = s
			}
		case "rule_height":
			ok = setFloat(&out.RuleHeight, v, 0, 20)
		case "next_label_ar", "next_label_en", "filler_label":
			s, good := jsonString(v)
			if ok = good && utf8.RuneCountInString(s) <= 60; ok {
				s = oneLine(s)
				switch name {
				case "next_label_ar":
					out.NextLabelAR = s
				case "next_label_en":
					out.NextLabelEN = s
				default:
					out.FillerLabel = s
				}
			}
		case "font_file":
			s, good := jsonString(v)
			if ok = good && !channel; ok {
				out.FontFile = strings.TrimSpace(s)
			}
		case "clocks":
			cl, good := parseClocks(v)
			if ok = good; ok {
				out.Clocks = cl
			}
		default:
			continue
		}
		if !ok {
			bad = append(bad, name)
		}
	}
	return out, bad
}

func setBool(dst *bool, v json.RawMessage) bool {
	var b bool
	if json.Unmarshal(v, &b) != nil {
		return false
	}
	*dst = b
	return true
}

func setFloat(dst *float64, v json.RawMessage, lo, hi float64) bool {
	var f float64
	if json.Unmarshal(v, &f) != nil || math.IsNaN(f) || f < lo || f > hi {
		return false
	}
	*dst = f
	return true
}

func setChoice(dst *string, v json.RawMessage, choices ...string) bool {
	s, ok := jsonString(v)
	if !ok {
		return false
	}
	for _, c := range choices {
		if s == c {
			*dst = s
			return true
		}
	}
	return false
}

// parseClocks reads the clock rows: objects {zone,label_ar,label_en} (how
// the settings store rows) or arrays [zone,label_ar,label_en]. An unknown
// zone fails the whole setting (the defaults stay).
func parseClocks(v json.RawMessage) ([]StripClock, bool) {
	var rows []json.RawMessage
	if json.Unmarshal(v, &rows) != nil || len(rows) > 12 {
		return nil, false
	}
	out := make([]StripClock, 0, len(rows))
	for _, r := range rows {
		var c StripClock
		if json.Unmarshal(r, &c) != nil {
			var a []string
			if json.Unmarshal(r, &a) != nil || len(a) == 0 {
				return nil, false
			}
			c.Zone = a[0]
			if len(a) > 1 {
				c.LabelAR = a[1]
			}
			if len(a) > 2 {
				c.LabelEN = a[2]
			}
		}
		c.Zone = strings.TrimSpace(c.Zone)
		if _, err := time.LoadLocation(c.Zone); err != nil || c.Zone == "" {
			return nil, false
		}
		c.LabelAR, c.LabelEN = oneLine(c.LabelAR), oneLine(c.LabelEN)
		out = append(out, c)
	}
	return out, true
}

func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
}

// ─── text shaping detection ───────────────────────────────────────────

var shapingCache sync.Map // ffmpeg binary → shapingResult

type shapingResult struct {
	ok  bool
	why string
}

// TextShaping reports whether this ffmpeg's drawtext shapes text
// (text_shaping, libharfbuzz + libfribidi) and renders an Arabic line with
// fontFile; the answer is cached per binary and font.
func TextShaping(ctx context.Context, ffmpeg, fontFile string) (bool, string) {
	key := ffmpeg + "\x00" + fontFile
	if r, ok := shapingCache.Load(key); ok {
		res := r.(shapingResult)
		return res.ok, res.why
	}
	res := probeShaping(ctx, ffmpeg, fontFile)
	shapingCache.Store(key, res)
	return res.ok, res.why
}

func probeShaping(ctx context.Context, ffmpeg, fontFile string) shapingResult {
	if fontFile == "" {
		return shapingResult{false, "no font file with Arabic letters found"}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	help, err := exec.CommandContext(cctx, ffmpeg, "-hide_banner", "-h", "filter=drawtext").CombinedOutput()
	if err != nil || !strings.Contains(string(help), "drawtext") {
		return shapingResult{false, "this ffmpeg has no drawtext filter"}
	}
	if !strings.Contains(string(help), "text_shaping") {
		return shapingResult{false, "this ffmpeg's drawtext cannot shape text (built without libfribidi/libharfbuzz)"}
	}
	dir, err := os.MkdirTemp("", "maktaba-strip-probe-")
	if err != nil {
		return shapingResult{false, err.Error()}
	}
	defer os.RemoveAll(dir)
	tf := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(tf, []byte("مكتبة"), 0o644); err != nil {
		return shapingResult{false, err.Error()}
	}
	vf := "drawtext=fontfile='" + escapeFilterPath(fontFile) + "':textfile='" + escapeFilterPath(tf) +
		"':expansion=none:text_shaping=1:fontcolor=white:fontsize=24:x=10:y=10"
	out, err := exec.CommandContext(cctx, ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=160x48:d=0.1", "-vf", vf, "-frames:v", "1", "-f", "null", "-").CombinedOutput()
	if err != nil {
		return shapingResult{false, "test render failed: " + lastLines(string(out), 3)}
	}
	return shapingResult{true, ""}
}

// FindStripFont returns the font for the strip: the setting, then
// MAKTABA_CHANNEL_STRIP_FONT, then a font with Arabic letters found on the
// host (Noto Naskh/Sans Arabic, Tahoma, Arial, Segoe UI, DejaVu Sans).
func FindStripFont(setting string) string {
	home, _ := os.UserHomeDir()
	cands := []string{setting, os.Getenv("MAKTABA_CHANNEL_STRIP_FONT"),
		"/usr/share/fonts/truetype/noto/NotoNaskhArabic-Regular.ttf",
		"/usr/share/fonts/truetype/noto/NotoSansArabic-Regular.ttf",
		"/usr/share/fonts/noto/NotoNaskhArabic-Regular.ttf",
		"/usr/share/fonts/noto/NotoSansArabic-Regular.ttf",
		"/usr/share/fonts/opentype/noto/NotoNaskhArabic-Regular.ttf",
		"/opt/homebrew/share/fonts/NotoNaskhArabic-Regular.ttf",
		filepath.Join(home, "Library/Fonts/NotoNaskhArabic-Regular.ttf"),
		"/Library/Fonts/NotoNaskhArabic-Regular.ttf",
		`C:/Windows/Fonts/NotoNaskhArabic-Regular.ttf`, `C:/Windows/Fonts/NotoSansArabic-Regular.ttf`,
		`C:/Windows/Fonts/tahoma.ttf`, `C:/Windows/Fonts/arial.ttf`, `C:/Windows/Fonts/segoeui.ttf`,
		"/System/Library/Fonts/Supplemental/Arial.ttf", "/Library/Fonts/Arial.ttf",
		"/System/Library/Fonts/Supplemental/Tahoma.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/usr/share/fonts/dejavu/DejaVuSans.ttf",
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// ─── what the strip says ──────────────────────────────────────────────

// BlockInfo is the display part of a block's title_snapshot.
type BlockInfo struct {
	Title  string // snapshot title (display title with use_in_guide)
	OnAir  string // the strip line (series · episode — title)
	Series string
	// Ref is the reference of a Quran verse the title quotes («البقرة ١٨٣»):
	// with overflow cycle the strip shows it after the verse.
	Ref string
}

// Line is what the strip shows for this block: the on-air line, else
// the title.
func (i BlockInfo) Line() string {
	if s := oneLine(i.OnAir); s != "" {
		return s
	}
	return oneLine(i.Title)
}

// ParseBlockInfo reads the display keys of a title_snapshot blob.
func ParseBlockInfo(raw []byte) BlockInfo {
	var s struct {
		Title      string `json:"title"`
		OnAir      string `json:"on_air"`
		StripTitle string `json:"strip_title"`
		Series     string `json:"strip_series"`
		Ref        string `json:"strip_ref"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	return BlockInfo{Title: s.Title, OnAir: s.OnAir, Series: s.Series, Ref: oneLine(s.Ref)}
}

// StripTexts are the strip's two lines for one segment.
type StripTexts struct {
	Now, Next string
	// NowRef / NextRef: a quoted verse's reference, shown after the line
	// when it is paged (overflow cycle).
	NowRef, NextRef string
	RTL             bool
}

// TextsFor decides what the strip says during seg: the programme's line
// (filler: the clip's title or the filler label; live: the broadcast's
// title; a slate: nothing but what is next) and the next programme's.
func TextsFor(seg Segment, st StripSettings) StripTexts {
	var t StripTexts
	switch seg.Kind {
	case KindProgram, KindBumper, KindLive:
		t.Now = seg.Block.Info.Line()
		t.NowRef = seg.Block.Info.Ref
	case KindFiller:
		if st.FillerLabel != "" {
			t.Now = st.FillerLabel
		} else {
			t.Now = seg.Block.Info.Line()
			t.NowRef = seg.Block.Info.Ref
		}
	}
	if st.ShowNext && seg.Next != nil {
		t.Next = seg.Next.Info.Line()
		t.NextRef = seg.Next.Info.Ref
	}
	switch st.Direction {
	case "rtl":
		t.RTL = true
	case "ltr":
		t.RTL = false
	default:
		t.RTL = hasArabic(t.Now) || (t.Now == "" && hasArabic(t.Next))
	}
	if t.Next != "" {
		label := st.NextLabelEN
		if t.RTL {
			label = st.NextLabelAR
		}
		if label != "" {
			t.Next = label + ": " + t.Next
		}
	}
	return t
}

func hasArabic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Arabic, r) {
			return true
		}
	}
	return false
}

// ─── geometry ─────────────────────────────────────────────────────────

// StripLayout is the strip's geometry on the output frame (pixels).
type StripLayout struct {
	W, H         int
	Top          bool
	Y, Height    int // the bar
	Pad          int
	ClockW       int // the clock box (0: no clock)
	ClockX       int
	TitleL       int // the title area [TitleL, TitleR)
	TitleR       int
	NowSize      int
	NextSize     int
	LabelSize    int
	TimeSize     int
	NowY, NextY  int
	LabelY       int
	TimeY        int
	NameW        int // channel name box (estimated)
	IndSize      int // the page indicator's font size
	RuleH        int // the accent rule's thickness (0: none)
	SingleLine   bool
	RTL          bool
	ClockOnRight bool
}

// Layout computes the strip's geometry for a profile.
func (st StripSettings) Layout(p OutputProfile, rtl, twoLines bool, channelName string) StripLayout {
	p = p.normalised()
	l := StripLayout{W: p.Width, H: p.Height, Top: st.Position == "top", RTL: rtl}
	l.Height = int(math.Round(float64(p.Height)*st.HeightPct/100)) &^ 1
	if l.Height < 16 {
		l.Height = 16
	}
	if !l.Top {
		l.Y = p.Height - l.Height
	}
	l.Pad = l.Height / 4
	if st.ShowClock && len(st.Clocks) > 0 {
		l.ClockW = l.Height * 19 / 10
	}
	// the clock at the strip's end: the right in a left-to-right strip, the left in a
	// right-to-left one
	l.ClockOnRight = !rtl
	l.TitleL, l.TitleR = l.Pad, p.Width-l.Pad
	if l.ClockW > 0 {
		if l.ClockOnRight {
			l.ClockX = p.Width - l.ClockW
			l.TitleR = l.ClockX - l.Pad
		} else {
			l.ClockX = 0
			l.TitleL = l.ClockW + l.Pad
		}
	}
	size := int(float64(l.Height) * st.FontSizePct / 100)
	if size < 8 {
		size = 8
	}
	l.SingleLine = !twoLines
	if l.SingleLine {
		l.NowSize = size
		l.NowY = l.Y + (l.Height-size)/2
	} else {
		l.NowSize = size
		l.NextSize = size * 2 / 3
		if l.NextSize < 8 {
			l.NextSize = 8
		}
		gap := (l.Height - l.NowSize - l.NextSize) / 3
		l.NowY = l.Y + gap
		l.NextY = l.NowY + l.NowSize + gap
	}
	if st.RuleHeight > 0 {
		l.RuleH = int(math.Round(float64(l.Height) * st.RuleHeight / 100))
		if l.RuleH < 1 {
			l.RuleH = 1
		}
	}
	l.IndSize = l.NextSize
	if l.IndSize == 0 || l.IndSize > l.NowSize*2/3 {
		l.IndSize = l.NowSize * 2 / 3
	}
	l.LabelSize = l.Height * 26 / 100
	l.TimeSize = l.Height * 40 / 100
	g := (l.Height - l.LabelSize - l.TimeSize) / 3
	l.LabelY = l.Y + g
	l.TimeY = l.LabelY + l.LabelSize + g
	if st.ShowChannelName && channelName != "" {
		l.NameW = int(float64(utf8.RuneCountInString(channelName))*float64(size)*0.55) + 2*l.Pad
		if l.NameW > p.Width/4 {
			l.NameW = p.Width / 4
		}
		if rtl {
			l.TitleR -= l.NameW + l.Pad
		} else {
			l.TitleL += l.NameW + l.Pad
		}
	}
	return l
}

// fitChars is about how many characters of a font size fit in width px.
func fitChars(width, size int) int {
	if size <= 0 {
		return 0
	}
	return int(float64(width) / (float64(size) * 0.52))
}

// rlm is the right-to-left mark: drawtext lays a line out left to right, so
// a right-to-left line is wrapped in marks to keep its punctuation and
// numbers at the right ends («ملحدون!», «١:٥٨ ص»).
const rlm = "\u200f"

func bidiWrap(s string, rtl bool) string {
	if !rtl || s == "" {
		return s
	}
	return rlm + s + rlm
}

// truncateText cuts s to at most limit runes at a word boundary, with "…".
func truncateText(s string, limit int) string {
	if limit <= 1 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	cut := string(r[:limit-1])
	if i := strings.LastIndex(cut, " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " -–—·:،,") + "…"
}

// ─── the clock ────────────────────────────────────────────────────────

// ClockStep is one change of the clock: from At (seconds into the block)
// it shows Label and Time.
type ClockStep struct {
	At    float64
	Zone  int
	Label string
	Time  string
}

// ClockSchedule is the clock's text over a block that starts on air at
// start and lasts dur: the city changes every `every` of wall time (the
// rotation follows the wall clock, so it carries on across blocks), and
// the time is refreshed at every minute.
func ClockSchedule(st StripSettings, rtl bool, start time.Time, dur time.Duration) []ClockStep {
	if len(st.Clocks) == 0 || dur <= 0 {
		return nil
	}
	every := time.Duration(st.ClockSeconds) * time.Second
	if every <= 0 {
		every = 8 * time.Second
	}
	locs := make([]*time.Location, len(st.Clocks))
	for i, c := range st.Clocks {
		loc, err := time.LoadLocation(c.Zone)
		if err != nil {
			loc = time.UTC
		}
		locs[i] = loc
	}
	arabicDigits := st.Digits == "arabic" || (st.Digits == "auto" && rtl)
	end := start.Add(dur)
	var out []ClockStep
	t := start
	const maxSteps = 50000
	for !t.After(end) && len(out) < maxSteps {
		zi := int((t.UnixNano() / int64(every)) % int64(len(st.Clocks)))
		if zi < 0 {
			zi += len(st.Clocks)
		}
		c := st.Clocks[zi]
		label := c.LabelEN
		if rtl && c.LabelAR != "" || label == "" {
			label = c.LabelAR
		}
		if label == "" {
			label = c.Zone
		}
		step := ClockStep{At: t.Sub(start).Seconds(), Zone: zi, Label: bidiWrap(label, hasArabic(label)),
			Time: bidiWrap(formatClock(t.In(locs[zi]), st.ClockFormat, rtl, arabicDigits), rtl)}
		if n := len(out); n == 0 || out[n-1].Label != step.Label || out[n-1].Time != step.Time {
			out = append(out, step)
		}
		// next change: the next rotation boundary or the next minute, whichever first
		nextRot := time.Unix(0, (t.UnixNano()/int64(every)+1)*int64(every))
		nextMin := t.Truncate(time.Minute).Add(time.Minute)
		if nextMin.Before(nextRot) {
			t = nextMin
		} else {
			t = nextRot
		}
	}
	return out
}

func formatClock(t time.Time, format string, rtl, arabicDigits bool) string {
	var s string
	if format == "12h" {
		h := t.Hour() % 12
		if h == 0 {
			h = 12
		}
		suffix := "AM"
		if t.Hour() >= 12 {
			suffix = "PM"
		}
		if rtl {
			suffix = "ص"
			if t.Hour() >= 12 {
				suffix = "م"
			}
		}
		s = fmt.Sprintf("%d:%02d %s", h, t.Minute(), suffix)
	} else {
		s = fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	}
	if arabicDigits {
		s = toArabicDigits(s)
	}
	return s
}

func toArabicDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune('٠' + (r - '0'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeReinit makes a text safe as the value of a drawtext reinit
// argument inside a single-quoted sendcmd token: the option parser
// unescapes \ and :, the quotes protect spaces, commas and semicolons.
func escapeReinit(s string) string {
	s = strings.NewReplacer("'", "’", "\n", " ", "\r", " ").Replace(s)
	return strings.NewReplacer(`\`, `\\`, ":", `\:`).Replace(s)
}

// SendCmdScript is the sendcmd file that switches the clock's two
// drawtext instances (drawtext@clkl, drawtext@clkt) at each step.
func SendCmdScript(steps []ClockStep) string { return CmdScript(clockCmds(steps)) }

// ─── the filter chain ─────────────────────────────────────────────────

// StripInput is everything BlockArgs needs to draw the strip of one block:
// the settings, the layout and the files written for it.
type StripInput struct {
	Settings StripSettings
	Layout   StripLayout
	FontFile string
	NowFile  string // UTF-8 text files, one line each ("" = no line)
	NextFile string
	NameFile string
	CmdFile  string // sendcmd script: the clock and the paged lines ("" = none)
	// NowPaged / NextPaged: the line is paged by the sendcmd timeline
	// (drawtext@now / drawtext@next, with drawtext@nowi / @nexti «١/٢»).
	NowPaged, NextPaged bool
	Clock               bool // the clock is drawn (drawtext@clkl / @clkt)
	// OffsetSec is how far into the programme the block starts (the seek):
	// the interval mode counts from the programme's start.
	OffsetSec float64
}

// ffColor renders #RRGGBB(+opacity) as an ffmpeg colour.
func ffColor(hex string, opacity float64) string {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		h = "000000"
	}
	if opacity >= 1 {
		return "0x" + h
	}
	return "0x" + h + "@" + strconv.FormatFloat(math.Max(0, opacity), 'f', 2, 64)
}

// enableExpr is the timeline expression of the strip ("" = always).
func (in StripInput) enableExpr() string {
	st := in.Settings
	if st.ShowMode != "interval" {
		return ""
	}
	every := st.EveryMinutes * 60
	if every <= 0 {
		every = 600
	}
	show := st.ShowSeconds
	if show <= 0 {
		show = 20
	}
	return "lt(mod(t+" + formatSec(in.OffsetSec) + "\\," + itoa(every) + ")\\," + itoa(show) + ")"
}

// textX is the x expression of a line in the title area: aligned to the
// start side (right in RTL), or scrolling when it is longer than the area.
func (in StripInput) textX() string {
	l := in.Layout
	area := itoa(l.TitleR - l.TitleL)
	var aligned string
	if l.RTL {
		aligned = itoa(l.TitleR) + "-tw"
	} else {
		aligned = itoa(l.TitleL)
	}
	if in.Settings.Overflow != "scroll" {
		return aligned
	}
	speed := itoa(in.Settings.ScrollSpeed)
	span := "(" + area + "+tw)"
	var scroll string
	if l.RTL { // Arabic tickers run left to right
		scroll = itoa(l.TitleL) + "-tw+mod(t*" + speed + "\\," + span + ")"
	} else {
		scroll = itoa(l.TitleR) + "-mod(t*" + speed + "\\," + span + ")"
	}
	return "if(lte(tw\\," + area + ")\\," + aligned + "\\," + scroll + ")"
}

// Filters is the strip's filter chain (comma-joined filters, no labels).
// The sendcmd that switches the clock's and the paged lines' texts comes
// right after the bar, before every drawtext it drives.
func (in StripInput) Filters() string {
	l, st := in.Layout, in.Settings
	font := "fontfile='" + escapeFilterPath(in.FontFile) + "'"
	en := ""
	if e := in.enableExpr(); e != "" {
		en = ":enable=" + e // commas escaped for the graph level, no quotes
	}
	color := ffColor(st.TextColor, 1)
	var f []string
	f = append(f, "drawbox=x=0:y="+itoa(l.Y)+":w=iw:h="+itoa(l.Height)+":color="+ffColor(st.BgColor, st.BgOpacity)+":t=fill"+en)
	if l.RuleH > 0 { // the accent rule along the edge facing the picture
		ry := l.Y
		if l.Top {
			ry = l.Y + l.Height - l.RuleH
		}
		f = append(f, "drawbox=x=0:y="+itoa(ry)+":w=iw:h="+itoa(l.RuleH)+":color="+ffColor(st.AccentColor, 1)+":t=fill"+en)
	}
	if in.CmdFile != "" {
		f = append(f, "sendcmd=f='"+escapeFilterPath(in.CmdFile)+"'")
	}
	// a line from its file, or (paged) a named drawtext whose text the
	// sendcmd timeline sets
	text := func(name string, paged bool, file string, size, y int, x, color string) string {
		src := ":textfile='" + escapeFilterPath(file) + "'"
		head := "drawtext="
		if paged {
			src, head = ":text=.", "drawtext@"+name+"="
		}
		return head + font + src + ":expansion=none:text_shaping=1" +
			":fontcolor=" + color + ":fontsize=" + itoa(size) + ":x=" + x + ":y=" + itoa(y) + en
	}
	indX := itoa(l.TitleR) + "-tw" // the page indicator: the line's far end
	if l.RTL {
		indX = itoa(l.TitleL)
	}
	if in.NowFile != "" || in.NowPaged {
		f = append(f, text("now", in.NowPaged, in.NowFile, l.NowSize, l.NowY, in.textX(), color))
		if in.NowPaged && st.PageIndicator {
			f = append(f, text("nowi", true, "", l.IndSize, l.NowY+(l.NowSize-l.IndSize)/2, indX, ffColor(st.IndicatorColor, 1)))
		}
	}
	if in.NextFile != "" || in.NextPaged {
		x := itoa(l.TitleL)
		if l.RTL {
			x = itoa(l.TitleR) + "-tw"
		}
		f = append(f, text("next", in.NextPaged, in.NextFile, l.NextSize, l.NextY, x, ffColor(st.NextColor, 1)))
		if in.NextPaged && st.PageIndicator {
			f = append(f, text("nexti", true, "", l.IndSize, l.NextY+(l.NextSize-l.IndSize)/2, indX, ffColor(st.IndicatorColor, 1)))
		}
	}
	onAccent := ffColor(st.ClockTextColor, 1)
	// drawn after the titles so a scrolling title passes under them
	if in.NameFile != "" && l.NameW > 0 {
		nx := itoa(l.Pad)
		if l.RTL {
			nx = itoa(l.W - l.Pad - l.NameW)
			if l.ClockOnRight && l.ClockW > 0 {
				nx = itoa(l.ClockX - l.Pad - l.NameW)
			}
		} else if !l.ClockOnRight && l.ClockW > 0 {
			nx = itoa(l.ClockW + l.Pad)
		}
		f = append(f, "drawbox=x="+nx+":y="+itoa(l.Y)+":w="+itoa(l.NameW)+":h="+itoa(l.Height)+
			":color="+ffColor(st.AccentColor, 1)+":t=fill"+en,
			"drawtext="+font+":textfile='"+escapeFilterPath(in.NameFile)+"':expansion=none:text_shaping=1"+
				":fontcolor="+onAccent+":fontsize="+itoa(l.NowSize)+":x="+nx+"+("+itoa(l.NameW)+"-tw)/2:y="+itoa(l.Y+(l.Height-l.NowSize)/2)+en)
	}
	if in.Clock && l.ClockW > 0 {
		cx := itoa(l.ClockX)
		mid := cx + "+(" + itoa(l.ClockW) + "-tw)/2"
		f = append(f,
			"drawbox=x="+cx+":y="+itoa(l.Y)+":w="+itoa(l.ClockW)+":h="+itoa(l.Height)+":color="+ffColor(st.AccentColor, 1)+":t=fill"+en,
			"drawtext@clkl="+font+":text=.:expansion=none:text_shaping=1:fontcolor="+onAccent+":fontsize="+itoa(l.LabelSize)+":x="+mid+":y="+itoa(l.LabelY)+en,
			"drawtext@clkt="+font+":text=.:expansion=none:text_shaping=1:fontcolor="+onAccent+":fontsize="+itoa(l.TimeSize)+":x="+mid+":y="+itoa(l.TimeY)+en)
	}
	return strings.Join(f, ",")
}

// BuildStrip writes the block's strip files under dir (named by key, so a
// retry of the same block reuses them) and returns the StripInput, or nil
// when the strip has nothing to show. The files are the caller's to remove.
func BuildStrip(p OutputProfile, st StripSettings, fontFile, channelName string, seg Segment,
	start time.Time, dur time.Duration, offsetSec float64, dir, key string) (*StripInput, []string, error) {
	texts := TextsFor(seg, st)
	if texts.Now == "" && texts.Next == "" {
		return nil, nil, nil
	}
	twoLines := texts.Now != "" && texts.Next != ""
	l := st.Layout(p, texts.RTL, twoLines, channelName)
	in := &StripInput{Settings: st, Layout: l, FontFile: fontFile, OffsetSec: offsetSec}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	var files []string
	write := func(name, text string) (string, error) {
		path := filepath.Join(dir, key+"-"+name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return "", err
		}
		files = append(files, path)
		return path, nil
	}
	area := l.TitleR - l.TitleL
	arabicDigits := st.Digits == "arabic" || (st.Digits == "auto" && texts.RTL)
	secs := dur.Seconds()
	base := float64(st.PageSeconds)
	if base <= 0 {
		base = 4
	}
	var cmds []StripCmd
	var err error
	// line puts one line on the strip: its file, or (overflow cycle, longer
	// than the strip) its pages on the timeline. It reports whether it pages.
	line := func(name, text, ref string, size int, pace float64) (string, bool, error) {
		if st.Overflow != "cycle" {
			if st.Overflow != "scroll" || name != "now" {
				text = truncateText(text, fitChars(area, size))
			}
			f, err := write(name+".txt", bidiWrap(text, texts.RTL))
			return f, false, err
		}
		pages := withRef(PageText(text, area, size), ref, area, size)
		if len(pages) > 1 {
			w := area
			if st.PageIndicator {
				w -= int(textWidth("٠٠/٠٠", l.IndSize)) + l.Pad // room for «١/٢»
			}
			pages = withRef(PageText(text, w, size), ref, w, size)
		}
		if len(pages) == 1 {
			f, err := write(name+".txt", bidiWrap(pages[0], texts.RTL))
			return f, false, err
		}
		for i := range pages {
			pages[i] = bidiWrap(pages[i], texts.RTL)
		}
		ind := ""
		if st.PageIndicator {
			ind = "drawtext@" + name + "i"
		}
		cmds = append(cmds, PageSchedule("drawtext@"+name, ind, pages, base*pace, secs, area, size, arabicDigits)...)
		return "", true, nil
	}
	nowLine, nowRef := texts.Now, texts.NowRef
	if l.SingleLine && nowLine == "" {
		nowLine, nowRef = texts.Next, texts.NextRef // a slate: only what is next, on the main line
		texts.Next = ""
	}
	if nowLine != "" {
		if in.NowFile, in.NowPaged, err = line("now", nowLine, nowRef, l.NowSize, 1); err != nil {
			return nil, files, err
		}
	}
	if texts.Next != "" {
		// the next line pages on its own, more slowly
		if in.NextFile, in.NextPaged, err = line("next", texts.Next, texts.NextRef, l.NextSize, 1.5); err != nil {
			return nil, files, err
		}
	}
	if st.ShowChannelName && channelName != "" && l.NameW > 0 {
		if in.NameFile, err = write("name.txt", bidiWrap(truncateText(oneLine(channelName), fitChars(l.NameW-2*l.Pad, l.NowSize)), hasArabic(channelName))); err != nil {
			return nil, files, err
		}
	}
	if l.ClockW > 0 {
		if steps := ClockSchedule(st, texts.RTL, start, dur); len(steps) > 0 {
			cmds = append(cmds, clockCmds(steps)...)
			in.Clock = true
		}
	}
	if len(cmds) > 0 {
		if in.CmdFile, err = write("strip.cmd", CmdScript(cmds)); err != nil {
			return nil, files, err
		}
	}
	return in, files, nil
}

// avoidStrip moves a watermark off the strip: a watermark on the strip's
// edge sits above (bottom strip) or below (top strip) it.
func avoidStrip(position string, margin int, s *StripInput) (string, string) {
	x, y := overlayXY(position, margin)
	if s == nil {
		return x, y
	}
	h := s.Layout.Height
	bottom := strings.HasPrefix(position, "bottom")
	if !s.Layout.Top && bottom {
		y = "H-h-" + itoa(margin+h)
	} else if s.Layout.Top && !bottom {
		y = itoa(margin + h)
	}
	return x, y
}
