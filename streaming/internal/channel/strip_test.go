package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func stripOn() StripSettings {
	st := DefaultStripSettings()
	st.Enabled = true
	return st
}

func TestApplyStripRulesChannelOverGlobal(t *testing.T) {
	global, bad := ApplyStripRules(DefaultStripSettings(),
		[]byte(`{"enabled":true,"position":"top","font_file":"/fonts/a.ttf","clock_seconds":5}`), false)
	if len(bad) != 0 || !global.Enabled || global.Position != "top" || global.FontFile != "/fonts/a.ttf" || global.ClockSeconds != 5 {
		t.Fatalf("global: %+v bad=%v", global, bad)
	}
	ch, bad := ApplyStripRules(global, []byte(`{"enabled":false,"position":"bottom","font_file":"/etc/passwd",
		"height_pct":99,"bg_color":"red","clocks":[{"zone":"Asia/Riyadh","label_ar":"مكة","label_en":"Makkah"},["UTC","غرينتش","GMT"]]}`), true)
	if ch.Enabled || ch.Position != "bottom" {
		t.Fatalf("channel switch/position not applied: %+v", ch)
	}
	if ch.FontFile != "/fonts/a.ttf" {
		t.Fatalf("a channel must not set the font file, got %q", ch.FontFile)
	}
	if ch.HeightPct != global.HeightPct || ch.BgColor != global.BgColor {
		t.Fatalf("bad values must keep the base: %+v", ch)
	}
	want := map[string]bool{"font_file": true, "height_pct": true, "bg_color": true}
	for _, b := range bad {
		delete(want, b)
	}
	if len(want) != 0 {
		t.Fatalf("bad = %v, missing %v", bad, want)
	}
	if len(ch.Clocks) != 2 || ch.Clocks[1].Zone != "UTC" || ch.Clocks[1].LabelEN != "GMT" {
		t.Fatalf("clocks: %+v", ch.Clocks)
	}
	if _, bad := ApplyStripRules(global, []byte(`{"clocks":[{"zone":"Mars/Olympus"}]}`), true); len(bad) != 1 {
		t.Fatalf("an unknown zone must be refused, bad=%v", bad)
	}
}

func seg(kind, onAir string, next *ProgramBlock) Segment {
	return Segment{Kind: kind, Block: ProgramBlock{Kind: kind, Info: BlockInfo{OnAir: onAir}}, Next: next}
}

func TestTextsFor(t *testing.T) {
	st := stripOn()
	next := &ProgramBlock{Kind: KindProgram, Info: BlockInfo{OnAir: "الإيمان سؤال وجواب · الحلقة 15"}}
	got := TextsFor(seg(KindProgram, "من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون!", next), st)
	if !got.RTL || got.Next != "التالي: الإيمان سؤال وجواب · الحلقة 15" || !strings.HasPrefix(got.Now, "من زاوية") {
		t.Fatalf("program: %+v", got)
	}
	st.FillerLabel = "فواصل"
	if got := TextsFor(seg(KindFiller, "clip title", nil), st); got.Now != "فواصل" {
		t.Fatalf("filler label: %+v", got)
	}
	st.FillerLabel = ""
	if got := TextsFor(seg(KindFiller, "A short clip", nil), st); got.Now != "A short clip" || got.RTL {
		t.Fatalf("filler title: %+v", got)
	}
	en := &ProgramBlock{Kind: KindProgram, Info: BlockInfo{Title: "The Story of Yusuf"}}
	if got := TextsFor(seg(KindSlate, "", en), st); got.Now != "" || got.Next != "Next: The Story of Yusuf" {
		t.Fatalf("slate: %+v", got)
	}
	st.ShowNext = false
	if got := TextsFor(seg(KindLive, "Al Jazeera live", en), st); got.Next != "" || got.Now != "Al Jazeera live" {
		t.Fatalf("live without next: %+v", got)
	}
}

func TestNextProgrammeSkipsFiller(t *testing.T) {
	blocks := []ProgramBlock{{Kind: KindProgram}, {Kind: KindFiller, Info: BlockInfo{Title: "clip"}},
		{Kind: KindProgram, Info: BlockInfo{Title: "B"}}}
	if n := nextProgramme(blocks, 1); n == nil || n.Info.Title != "B" {
		t.Fatalf("next = %+v", n)
	}
	if n := nextProgramme(blocks[:2], 1); n == nil || n.Info.Title != "clip" {
		t.Fatalf("no programme ahead: the titled filler, got %+v", n)
	}
}

func TestLayoutClockSideAndWatermark(t *testing.T) {
	p := DefaultProfile()
	st := stripOn()
	rtl := st.Layout(p, true, true, "")
	if rtl.ClockX != 0 || rtl.TitleL < rtl.ClockW || rtl.ClockOnRight {
		t.Fatalf("RTL: clock on the left, titles right of it: %+v", rtl)
	}
	ltr := st.Layout(p, false, true, "")
	if ltr.ClockX != p.Width-ltr.ClockW || ltr.TitleR > ltr.ClockX {
		t.Fatalf("LTR: clock on the right: %+v", ltr)
	}
	if rtl.Y != p.Height-rtl.Height || rtl.Height != 96 {
		t.Fatalf("bottom 9%% of 1080: y=%d h=%d", rtl.Y, rtl.Height)
	}
	in := &StripInput{Layout: rtl}
	if _, y := avoidStrip("bottom-right", 30, in); y != "H-h-126" {
		t.Fatalf("a bottom watermark sits above a bottom strip, y=%s", y)
	}
	if _, y := avoidStrip("top-right", 30, in); y != "30" {
		t.Fatalf("a top watermark is untouched by a bottom strip, y=%s", y)
	}
	st.Position = "top"
	top := &StripInput{Layout: st.Layout(p, true, false, "")}
	if _, y := avoidStrip("top-left", 30, top); y != "126" {
		t.Fatalf("a top watermark sits below a top strip, y=%s", y)
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestZoneOffsetsAndDST2026(t *testing.T) {
	cases := []struct {
		zone string
		at   string
		off  int // hours
	}{
		{"Asia/Riyadh", "2026-01-15T12:00:00Z", 3},
		{"Asia/Riyadh", "2026-07-15T12:00:00Z", 3}, // no DST
		{"UTC", "2026-07-15T12:00:00Z", 0},
		{"America/New_York", "2026-03-08T06:59:00Z", -5}, // EST until 02:00 local
		{"America/New_York", "2026-03-08T07:00:00Z", -4}, // EDT from 03:00 local
		{"America/New_York", "2026-11-01T05:59:00Z", -4},
		{"America/New_York", "2026-11-01T06:00:00Z", -5},
		{"Africa/Cairo", "2026-04-23T21:59:00Z", 2}, // EET until the last Friday of April
		{"Africa/Cairo", "2026-04-23T22:00:00Z", 3}, // EEST
		{"Africa/Cairo", "2026-10-29T20:59:00Z", 3}, // until the last Thursday of October
		{"Africa/Cairo", "2026-10-29T21:00:00Z", 2},
	}
	for _, c := range cases {
		at, _ := time.Parse(time.RFC3339, c.at)
		_, off := at.In(mustLoc(t, c.zone)).Zone()
		if off != c.off*3600 {
			t.Errorf("%s at %s: offset %d h, want %d", c.zone, c.at, off/3600, c.off)
		}
	}
}

func TestClockScheduleRotationAndMinutes(t *testing.T) {
	st := stripOn()
	start, _ := time.Parse(time.RFC3339, "2026-06-01T09:00:03Z")
	steps := ClockSchedule(st, true, start, 70*time.Second)
	if len(steps) == 0 || steps[0].At != 0 {
		t.Fatalf("steps: %+v", steps)
	}
	// 09:00:03 → 8 s slots since the epoch: slot index decides the city
	zi := int((start.Unix() / 8) % 4)
	if steps[0].Zone != zi {
		t.Fatalf("first zone %d, want %d", steps[0].Zone, zi)
	}
	for i, s := range steps[1:] {
		wall := start.Add(time.Duration(s.At * float64(time.Second)))
		if wall.Unix()%8 != 0 && wall.Second() != 0 {
			t.Fatalf("step %d at %v is neither a rotation boundary nor a minute", i+1, wall)
		}
	}
	// Makkah 12:00 in Arabic-Indic digits, Cairo 12:00 (EEST, +3) in June, New York 05:00
	want := map[string]string{"مكة المكرمة": "١٢:٠٠", "القاهرة": "١٢:٠٠", "غرينتش": "٠٩:٠٠", "نيويورك": "٠٥:٠٠"}
	seen := map[string]bool{}
	for _, s := range steps {
		label, tm := strings.Trim(s.Label, rlm), strings.Trim(s.Time, rlm)
		if s.At < 57 { // before 09:01
			if w := want[label]; w != tm {
				t.Errorf("%s shows %s, want %s", label, tm, w)
			}
			seen[label] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("every city within 32 s, saw %v", seen)
	}
	// the minute turns at 57 s into the block: the city on air then shows :01
	for _, s := range steps {
		if s.At >= 57 && !strings.HasSuffix(strings.Trim(s.Time, rlm), "٠١") {
			t.Errorf("after the minute: %+v", s)
		}
	}
}

func TestClockScheduleAcrossNewYorkDST(t *testing.T) {
	st := stripOn()
	st.Clocks = []StripClock{{Zone: "America/New_York", LabelAR: "نيويورك", LabelEN: "New York"}}
	st.Digits = "western"
	start, _ := time.Parse(time.RFC3339, "2026-03-08T06:58:00Z") // 01:58 EST
	steps := ClockSchedule(st, false, start, 3*time.Minute)
	var times []string
	for _, s := range steps {
		times = append(times, s.Time)
	}
	got := strings.Join(times, " ")
	if got != "01:58 01:59 03:00 03:01" {
		t.Fatalf("times across the change: %q", got)
	}
	st.ClockFormat = "12h"
	if s := ClockSchedule(st, true, start, time.Second); s[0].Time != rlm+"1:58 ص"+rlm || s[0].Label != rlm+"نيويورك"+rlm {
		t.Fatalf("12 h Arabic: %+v", s[0])
	}
}

func TestSendCmdScriptEscaping(t *testing.T) {
	got := SendCmdScript([]ClockStep{{At: 8, Label: "Saint John's", Time: "12:05"}})
	want := "8.000 drawtext@clkl reinit 'text=Saint John’s', drawtext@clkt reinit 'text=12\\:05';\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestStripFiltersNeverCarryTitles(t *testing.T) {
	dir := t.TempDir()
	title := "كيف نخاطب الجيل الجديد؟ 100% : 'quoted' , [x]"
	s := seg(KindProgram, title, &ProgramBlock{Kind: KindProgram, Info: BlockInfo{OnAir: "التالي"}})
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	st := stripOn()
	st.Overflow = "scroll"
	st.ShowMode = "interval"
	in, files, err := BuildStrip(DefaultProfile(), st, "C:/Windows/Fonts/arial.ttf", "قناة", s, start, time.Minute, 125, dir, "k")
	if err != nil || in == nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files: %v", files)
	}
	b, _ := os.ReadFile(in.NowFile)
	if string(b) != rlm+title+rlm {
		t.Fatalf("the title goes to the file as it is: %q", b)
	}
	f := in.Filters()
	for _, bad := range []string{"نخاطب", "100%", "quoted"} {
		if strings.Contains(f, bad) {
			t.Fatalf("filter string carries title text %q: %s", bad, f)
		}
	}
	for _, want := range []string{"textfile='", "expansion=none", "text_shaping=1", "fontfile='C\\:/Windows/Fonts/arial.ttf'",
		"sendcmd=f='", "drawtext@clkl=", "enable=lt(mod(t+125\\,600)\\,20)", "if(lte(tw\\,", "-tw+mod(t*90\\,"} {
		if !strings.Contains(f, want) {
			t.Fatalf("filter misses %q:\n%s", want, f)
		}
	}
	args := strings.Join(BlockArgs(DefaultProfile(), BlockInput{Kind: "file", Source: "a.mp4", DurationSec: 60,
		HasVideo: true, HasAudio: true, Strip: in,
		Watermark: &Watermark{Path: "wm.png", Position: "bottom-left", WidthPct: 10, MarginPct: 3}}), " ")
	if !strings.Contains(args, "[vo]drawbox=") || !strings.Contains(args, "-map [vs]") {
		t.Fatalf("strip after the watermark, mapped: %s", args)
	}
	if !strings.Contains(args, "y=H-h-128") {
		t.Fatalf("watermark lifted above the strip (32+96): %s", args)
	}
}

// TestStripRendersArabic renders a real 3 s block with the strip (Arabic,
// shaped) and checks the strip region changed against the same frame
// without it. Skipped when ffmpeg or a shaping drawtext is missing. With
// MAKTABA_STRIP_FRAMES=<dir> it saves one frame per clock city there.
func TestStripRendersArabic(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	font := FindStripFont("")
	if ok, why := TextShaping(context.Background(), ff, font); !ok {
		t.Skip("strip unavailable: " + why)
	}
	dir := t.TempDir()
	p := OutputProfile{Width: 960, Height: 540, FPS: 25, VideoKbps: 1500}
	st := stripOn()
	st.ClockSeconds = 2
	next := &ProgramBlock{Kind: KindProgram, Info: BlockInfo{OnAir: "الإيمان سؤال وجواب · الحلقة 15 — ثمرات عبادة الله"}}
	s := seg(KindProgram, "من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون!", next)
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC) // :00 → Makkah first (slot 0 of 4)
	frames := os.Getenv("MAKTABA_STRIP_FRAMES")
	dur := 3 * time.Second
	if frames != "" {
		dur = 8 * time.Second
	}
	in, _, err := BuildStrip(p, st, font, "مكتبة", s, start, dur, 0, dir, "t")
	if err != nil {
		t.Fatal(err)
	}
	render := func(strip *StripInput, at float64, out string) image.Image {
		filters := "scale=960:540,setsar=1,format=yuv420p"
		if strip != nil {
			filters += "," + strip.Filters()
		}
		args := append(append([]string{"-hide_banner", "-nostdin", "-loglevel", "error"}, frameBackground(dur.Seconds())...),
			"-vf", filters+",select=gte(t\\,"+formatSec(at)+")", "-frames:v", "1", "-y", out)
		cmd := exec.Command(ff, args...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, b)
		}
		fb, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(fb))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	plain := render(nil, 1, filepath.Join(dir, "plain.png"))
	with := render(in, 1, filepath.Join(dir, "strip.png"))
	l := in.Layout
	diff := 0
	for y := l.Y; y < l.Y+l.Height; y += 2 {
		for x := 0; x < l.W; x += 2 {
			r1, g1, b1, _ := plain.At(x, y).RGBA()
			r2, g2, b2, _ := with.At(x, y).RGBA()
			if absDiff(r1, r2)+absDiff(g1, g2)+absDiff(b1, b2) > 3000 {
				diff++
			}
		}
	}
	total := (l.Height / 2) * (l.W / 2)
	if diff < total/2 {
		t.Fatalf("strip region barely changed: %d of %d samples", diff, total)
	}
	// above the strip the picture is untouched
	r1, g1, b1, _ := plain.At(l.W/2, l.Y/2).RGBA()
	r2, g2, b2, _ := with.At(l.W/2, l.Y/2).RGBA()
	if absDiff(r1, r2)+absDiff(g1, g2)+absDiff(b1, b2) > 3000 {
		t.Fatal("the picture above the strip changed")
	}
	if frames != "" {
		_ = os.MkdirAll(frames, 0o755)
		for i, c := range st.Clocks {
			render(in, float64(i*2)+0.5, filepath.Join(frames, "strip-"+strings.ReplaceAll(c.LabelEN, " ", "")+frameSuffix()+".png"))
		}
		js, _ := json.MarshalIndent(ClockSchedule(st, true, start, dur), "", " ")
		_ = os.WriteFile(filepath.Join(frames, "clock-steps.json"), js, 0o644)
	}
}

// frameBackground is the picture the strip tests draw on: a test pattern,
// or with MAKTABA_STRIP_BG "dark" a plain dark frame, or a still image path.
// frameSuffix names saved frames by their background.
func frameSuffix() string {
	switch bg := os.Getenv("MAKTABA_STRIP_BG"); bg {
	case "":
		return ""
	case "dark":
		return "-dark"
	default:
		return "-still"
	}
}

func frameBackground(sec float64) []string {
	d := formatSec(sec)
	switch bg := os.Getenv("MAKTABA_STRIP_BG"); bg {
	case "":
		return []string{"-f", "lavfi", "-i", "testsrc2=s=960x540:r=25:d=" + d}
	case "dark":
		return []string{"-f", "lavfi", "-i", "color=c=0x101418:s=960x540:r=25:d=" + d}
	default:
		return []string{"-loop", "1", "-framerate", "25", "-t", d, "-i", bg}
	}
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// ─── paging (overflow cycle) ─────────────────────────────────────────

func stripWords(pages []string) []string {
	var out []string
	for _, p := range pages {
		p = strings.NewReplacer("﴿", "", "﴾", "", "{", "", "}", "", "(", "", ")", "").Replace(p)
		out = append(out, strings.Fields(p)...)
	}
	return out
}

func TestPageTextAtWordBoundaries(t *testing.T) {
	for _, s := range []string{
		"من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون! وكيف نتعامل مع الشبهات المعاصرة في زمن الإنترنت ووسائل التواصل",
		"The Story of Yusuf, part three: the dream of the king and the years of plenty and famine in Egypt explained",
	} {
		pages := PageText(s, 600, 40)
		if len(pages) < 2 {
			t.Fatalf("a long line pages: %q", pages)
		}
		if got, want := strings.Join(stripWords(pages), " "), strings.Join(strings.Fields(s), " "); got != want {
			t.Fatalf("every word once, in order:\n got %q\nwant %q", got, want)
		}
		for _, p := range pages {
			if textWidth(p, 40) > 600*widthMargin && len(strings.Fields(p)) > 1 {
				t.Fatalf("page too wide: %q", p)
			}
		}
	}
	if p := PageText("قصة يوسف", 600, 40); len(p) != 1 || p[0] != "قصة يوسف" {
		t.Fatalf("a short line is one page: %q", p)
	}
	if p := PageText("كلمةطويلةجداجداجداجداجداجداجداجداجداجداجداجدا قصيرة", 200, 40); p[0] != "كلمةطويلةجداجداجداجداجداجداجداجداجداجداجداجدا" {
		t.Fatalf("a word wider than a page is never split: %q", p)
	}
}

func TestPageTextVerseKeepsOrderAndBrackets(t *testing.T) {
	title := "تفسير قوله تعالى: ﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على الذين من قبلكم لعلكم تتقون﴾"
	pages := withRef(PageText(title, 520, 40), "البقرة ١٨٣", 520, 40)
	if len(pages) < 3 {
		t.Fatalf("pages: %q", pages)
	}
	for i, p := range pages {
		if strings.Count(p, "﴿") != strings.Count(p, "﴾") {
			t.Fatalf("page %d unbalanced: %q", i, p)
		}
	}
	if !strings.HasPrefix(pages[1], "﴿") {
		t.Fatalf("a continued verse reopens its bracket: %q", pages[1])
	}
	last := pages[len(pages)-1]
	if !strings.Contains(last, "البقرة ١٨٣") {
		t.Fatalf("the reference comes last: %q", pages)
	}
	if got := strings.Join(stripWords(pages), " "); !strings.HasPrefix(got, strings.Join(stripWords([]string{title}), " ")) {
		t.Fatalf("the whole verse, in order:\n%q", got)
	}
}

func TestPageScheduleCyclesThroughTheBlock(t *testing.T) {
	pages := []string{"أ ب", "ج د", "هـ و"}
	cmds := PageSchedule("drawtext@now", "drawtext@nowi", pages, 4, 30, 600, 40, true)
	var texts []string
	var labels []string
	last := -1.0
	for _, c := range cmds {
		if c.At < last {
			t.Fatal("times go forward")
		}
		last = c.At
		if c.Target == "drawtext@now" {
			texts = append(texts, c.Text)
		} else {
			labels = append(labels, c.Text)
		}
	}
	if cmds[0].At != 0 || texts[0] != "أ ب" || texts[3] != "أ ب" || labels[1] != "٢/٣" {
		t.Fatalf("cycle: %v %v", texts, labels)
	}
	if last >= 30 || last < 30-8 {
		t.Fatalf("the cycle runs to the block's end: last at %v", last)
	}
	slow := PageSchedule("drawtext@next", "", pages, 6, 30, 600, 40, false)
	if len(slow) >= len(texts) {
		t.Fatalf("the next line pages more slowly: %d vs %d", len(slow), len(texts))
	}
	if PageSchedule("x", "", []string{"one"}, 4, 30, 600, 40, false) != nil {
		t.Fatal("one page: no timeline")
	}
}

func TestCmdScriptGroupsInstants(t *testing.T) {
	got := CmdScript([]StripCmd{{At: 4, Target: "drawtext@now", Text: "ب: ج"}, {At: 0, Target: "drawtext@now", Text: "أ"},
		{At: 0, Target: "drawtext@clkl", Text: "مكة"}})
	want := "0.000 drawtext@now reinit 'text=أ', drawtext@clkl reinit 'text=مكة';\n4.000 drawtext@now reinit 'text=ب\\: ج';\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestBuildStripPagesALongTitle(t *testing.T) {
	long := "من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون! وكيف نتعامل مع الشبهات المعاصرة في زمن الإنترنت ووسائل التواصل الاجتماعي"
	next := &ProgramBlock{Kind: KindProgram, Info: BlockInfo{OnAir: "تفسير قوله تعالى: ﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على الذين من قبلكم لعلكم تتقون﴾", Ref: "البقرة ١٨٣"}}
	next.Info.OnAir += " — " + long // a next line longer than the strip too
	in, files, err := BuildStrip(DefaultProfile(), stripOn(), "C:/Windows/Fonts/arial.ttf", "", seg(KindProgram, long, next),
		time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC), 2*time.Minute, 0, t.TempDir(), "k")
	if err != nil || in == nil {
		t.Fatal(err)
	}
	if !in.NowPaged || !in.NextPaged || in.NowFile != "" || len(files) != 1 {
		t.Fatalf("both lines paged by the timeline: %+v files=%v", in, files)
	}
	b, _ := os.ReadFile(in.CmdFile)
	script := string(b)
	for _, want := range []string{"drawtext@now reinit", "drawtext@nowi reinit 'text=١/", "drawtext@next reinit", "البقرة ١٨٣", "drawtext@clkt reinit"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script misses %q", want)
		}
	}
	f := in.Filters()
	if strings.Contains(f, "الخنادق") || !strings.Contains(f, "drawtext@now=") || strings.Index(f, "sendcmd") > strings.Index(f, "drawtext@now=") {
		t.Fatalf("filters: %s", f)
	}
}

// TestStripRendersPages renders a strip whose title and a verse page in
// turn, and checks that the title area differs between two pages. With
// MAKTABA_STRIP_FRAMES=<dir> it saves a frame of every page there.
func TestStripRendersPages(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	font := FindStripFont("")
	if ok, why := TextShaping(context.Background(), ff, font); !ok {
		t.Skip("strip unavailable: " + why)
	}
	p := OutputProfile{Width: 960, Height: 540, FPS: 25, VideoKbps: 1500}
	cases := map[string]Segment{
		"title": seg(KindProgram, "من زاوية أخرى · الحلقة 45 — في الخنادق… لا يوجد ملحدون! وكيف نتعامل مع الشبهات المعاصرة",
			&ProgramBlock{Kind: KindProgram, Info: BlockInfo{OnAir: "الإيمان سؤال وجواب · الحلقة 15"}}),
		"verse": {Kind: KindProgram, Block: ProgramBlock{Kind: KindProgram, Info: BlockInfo{
			OnAir: "تفسير قوله تعالى: ﴿يا أيها الذين آمنوا كتب عليكم الصيام كما كتب على الذين من قبلكم لعلكم تتقون﴾",
			Ref:   "البقرة ١٨٣"}}},
	}
	frames := os.Getenv("MAKTABA_STRIP_FRAMES")
	for name, s := range cases {
		dir := t.TempDir()
		in, _, err := BuildStrip(p, stripOn(), font, "", s, time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
			30*time.Second, 0, dir, name)
		if err != nil || in == nil || !in.NowPaged {
			t.Fatalf("%s: not paged: %+v %v", name, in, err)
		}
		b, _ := os.ReadFile(in.CmdFile)
		var at []float64
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.Contains(ln, "drawtext@now reinit") {
				f, _ := strconv.ParseFloat(strings.Fields(ln)[0], 64)
				if len(at) == 0 || f > at[len(at)-1] {
					at = append(at, f)
				}
			}
		}
		pagesN := strings.Count(string(b), "drawtext@nowi reinit 'text=١/")
		if len(at) < 2 || pagesN < 1 {
			t.Fatalf("%s: no page timeline:\n%s", name, b)
		}
		var imgs []image.Image
		n := len(at)
		if n > 4 {
			n = 4
		}
		for i := 0; i < n; i++ {
			out := filepath.Join(dir, fmt.Sprintf("p%d.png", i))
			args := append(append([]string{"-hide_banner", "-nostdin", "-loglevel", "error"}, frameBackground(30)...),
				"-vf", "scale=960:540,setsar=1,format=yuv420p,"+in.Filters()+",select=gte(t\\,"+formatSec(at[i]+0.5)+")",
				"-frames:v", "1", "-y", out)
			cmd := exec.Command(ff, args...)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("ffmpeg: %v\n%s", err, b)
			}
			fb, _ := os.ReadFile(out)
			img, err := png.Decode(bytes.NewReader(fb))
			if err != nil {
				t.Fatal(err)
			}
			imgs = append(imgs, img)
			if frames != "" {
				_ = os.MkdirAll(frames, 0o755)
				_ = os.WriteFile(filepath.Join(frames, fmt.Sprintf("strip-%s-page%d%s.png", name, i+1, frameSuffix())), fb, 0o644)
			}
		}
		l := in.Layout
		diff := 0
		for y := l.NowY; y < l.NowY+l.NowSize; y++ {
			for x := l.TitleL; x < l.TitleR; x += 2 {
				r1, g1, b1, _ := imgs[0].At(x, y).RGBA()
				r2, g2, b2, _ := imgs[1].At(x, y).RGBA()
				if absDiff(r1, r2)+absDiff(g1, g2)+absDiff(b1, b2) > 6000 {
					diff++
				}
			}
		}
		if diff < 200 {
			t.Fatalf("%s: page 2 looks like page 1 (%d px differ)", name, diff)
		}
	}
}

// ─── palette ─────────────────────────────────────────────────────────

func relLum(hex string) float64 {
	var rgb [3]float64
	for i := 0; i < 3; i++ {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			rgb[i] = c / 12.92
		} else {
			rgb[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
}

func contrast(a, b string) float64 {
	la, lb := relLum(a), relLum(b)
	if lb > la {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestDefaultPaletteGreenAndGoldReadable(t *testing.T) {
	st := DefaultStripSettings()
	if st.BgColor != "#0b5d3b" || st.AccentColor != "#c9a227" {
		t.Fatalf("palette: %+v", st)
	}
	for _, c := range []struct{ fg, bg, what string }{
		{st.TextColor, st.BgColor, "title"}, {st.NextColor, st.BgColor, "next"},
		{st.IndicatorColor, st.BgColor, "page number"}, {st.ClockTextColor, st.AccentColor, "clock"},
	} {
		if r := contrast(c.fg, c.bg); r < 4.5 {
			t.Errorf("%s: contrast %.2f < 4.5", c.what, r)
		}
	}
}

func TestPaletteSettingsAndFilters(t *testing.T) {
	st, bad := ApplyStripRules(stripOn(), []byte(`{"next_color":"#112233","clock_text_color":"#000000",
		"indicator_color":"#ffcc00","rule_height":10,"text_color":"#zzzzzz"}`), true)
	if len(bad) != 1 || bad[0] != "text_color" || st.NextColor != "#112233" || st.RuleHeight != 10 {
		t.Fatalf("settings: %+v bad=%v", st, bad)
	}
	if _, bad := ApplyStripRules(st, []byte(`{"rule_height":25}`), true); len(bad) != 1 {
		t.Fatal("rule_height 0..20")
	}
	l := st.Layout(DefaultProfile(), true, true, "")
	in := StripInput{Settings: st, Layout: l, FontFile: "f.ttf", NowFile: "n.txt", NextFile: "x.txt", CmdFile: "c.cmd", Clock: true}
	f := in.Filters()
	for _, want := range []string{
		"drawbox=x=0:y=984:w=iw:h=10:color=0xc9a227:t=fill", // the gold rule on the bar's top edge
		"fontcolor=0x112233:fontsize=26",                    // the next line
		"drawtext@clkt=f", "fontcolor=0x000000:fontsize=38", // the clock on the gold
	} {
		if !strings.Contains(f, want) {
			t.Fatalf("filters miss %q:\n%s", want, f)
		}
	}
	st.Position = "top"
	top := StripInput{Settings: st, Layout: st.Layout(DefaultProfile(), true, false, ""), FontFile: "f.ttf", NowFile: "n.txt"}
	if !strings.Contains(top.Filters(), "drawbox=x=0:y=86:w=iw:h=10:") {
		t.Fatalf("a top bar's rule is on its lower edge: %s", top.Filters())
	}
	st.RuleHeight = 0
	if strings.Count(StripInput{Settings: st, Layout: st.Layout(DefaultProfile(), true, false, ""), NowFile: "n"}.Filters(), "drawbox") != 1 {
		t.Fatal("no rule at 0")
	}
}
