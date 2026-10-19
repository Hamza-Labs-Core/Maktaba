package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
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
	in, files, err := BuildStrip(DefaultProfile(), st, `C:\Windows\Fonts\arial.ttf`, "قناة", s, start, time.Minute, 125, dir, "k")
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
		filters := "testsrc2=s=960x540:r=25:d=" + formatSec(dur.Seconds()) + ",format=yuv420p"
		if strip != nil {
			filters += "," + strip.Filters()
		}
		cmd := exec.Command(ff, "-hide_banner", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", filters,
			"-vf", "select=gte(t\\,"+formatSec(at)+")", "-frames:v", "1", "-y", out)
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
			render(in, float64(i*2)+0.5, filepath.Join(frames, "strip-"+strings.ReplaceAll(c.LabelEN, " ", "")+".png"))
		}
		js, _ := json.MarshalIndent(ClockSchedule(st, true, start, dur), "", " ")
		_ = os.WriteFile(filepath.Join(frames, "clock-steps.json"), js, 0o644)
	}
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
