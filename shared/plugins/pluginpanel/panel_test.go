package pluginpanel

import (
	"strings"
	"testing"
)

// Story 54.20 T01: the panel schema corpus.
func TestParse(t *testing.T) {
	good := `{"ui":1,"title":"panel.title","blocks":[
		{"type":"kv","key":"loudness","label":"panel.loudness"},
		{"type":"badges","key":"flags"},
		{"type":"chips","key":"topics","label":"panel.topics"},
		{"type":"table","key":"scenes","columns":["col.start","col.what"]},
		{"type":"markers","key":"silences"},
		{"type":"image","label":"panel.waveform"},
		{"type":"text","key":"summary"}]}`
	p, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if p.Uses("scenes") == nil || p.Uses("other") != nil || len(p.Blocks) != 7 {
		t.Fatalf("%+v", p)
	}
	for name, raw := range map[string]string{
		"ui 2":            `{"ui":2,"title":"t","blocks":[{"type":"text","key":"a"}]}`,
		"unknown key":     `{"ui":1,"title":"t","blocks":[{"type":"text","key":"a"}],"html":"<b>"}`,
		"unknown type":    `{"ui":1,"title":"t","blocks":[{"type":"html","key":"a"}]}`,
		"no blocks":       `{"ui":1,"title":"t","blocks":[]}`,
		"no key":          `{"ui":1,"title":"t","blocks":[{"type":"kv"}]}`,
		"key twice":       `{"ui":1,"title":"t","blocks":[{"type":"kv","key":"a"},{"type":"text","key":"a"}]}`,
		"image with key":  `{"ui":1,"title":"t","blocks":[{"type":"image","key":"a"}]}`,
		"two images":      `{"ui":1,"title":"t","blocks":[{"type":"image"},{"type":"image"}]}`,
		"seven columns":   `{"ui":1,"title":"t","blocks":[{"type":"table","key":"a","columns":["a","b","c","d","e","f","g"]}]}`,
		"columns on kv":   `{"ui":1,"title":"t","blocks":[{"type":"kv","key":"a","columns":["a"]}]}`,
		"a title is text": `{"ui":1,"title":"Loudness report","blocks":[{"type":"text","key":"a"}]}`,
		"eleven blocks":   `{"ui":1,"title":"t","blocks":[` + strings.Repeat(`{"type":"chips","key":"k"},`, 10) + `{"type":"text","key":"z"}]}`,
	} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// T03: URLs, HTML, markdown and control characters are refused in any text a panel shows.
func TestForbidden(t *testing.T) {
	for _, s := range []string{
		"https://evil.example/x.png", "see HTTP://x", "www.example.com", "evil.example/x.png", "host.test:8080",
		"mailto:a@b", "javascript:alert(1)", "data:image/png;base64,AA", "<img src=x>", "</b>", "a &amp; b", "&#106;",
		"[click](x)", "![i](x)", "line\nbreak", "tab\there", "‮reversed", "⁧isolate", "\x00", "\uFEFFbom",
	} {
		if Forbidden(s) == "" {
			t.Errorf("%q: allowed", s)
		}
	}
	for _, s := range []string{
		"Loudness -14 LUFS", "الحلقة الثالثة: فقه الصيام", "3.5 dB", "Scene 1 / intro", "10:30", "50% louder",
		"a < b and c > d", "Q&A", "e.g. this", "[draft]",
	} {
		if why := Forbidden(s); why != "" {
			t.Errorf("%q: refused (%s)", s, why)
		}
	}
}

func TestCheckValue(t *testing.T) {
	p, _ := Parse([]byte(`{"ui":1,"title":"t","blocks":[{"type":"kv","key":"kv"},{"type":"badges","key":"b"},
		{"type":"chips","key":"c"},{"type":"table","key":"tb","columns":["x","y"]},{"type":"markers","key":"m"},{"type":"text","key":"t"}]}`))
	ok := map[string]string{
		"kv": `[{"label":"Integrated","value":"-14 LUFS"},{"label":"Peak","value":-1.2}]`,
		"b":  `[{"text":"Loud","tone":"warning"},{"text":"OK"}]`,
		"c":  `["fiqh","الصيام"]`,
		"tb": `[["00:01","intro"],[12,"talk"]]`,
		"m":  `[{"t_ms":1500,"label":"silence"}]`,
		"t":  `"A plain summary."`,
	}
	for k, v := range ok {
		if e := CheckValue(p.Uses(k), []byte(v)); e != nil {
			t.Errorf("%s: %v", k, e)
		}
	}
	for k, c := range map[string][2]string{
		"kv": {`[{"label":"Link","value":"https://evil.example/x.png"}]`, CodeTextForbidden},
		"b":  {`[{"text":"x","tone":"red"}]`, "plugin_invalid"},
		"c":  {`["<b>x</b>"]`, CodeTextForbidden},
		"tb": {`[["a","b","c"]]`, "plugin_invalid"},
		"m":  {`[{"t_ms":1.5,"label":"x"}]`, "plugin_invalid"},
		"t":  {`"` + strings.Repeat("a", 1001) + `"`, "plugin_too_large"},
	} {
		e := CheckValue(p.Uses(k), []byte(c[0]))
		if e == nil || e.Code != c[1] {
			t.Errorf("%s: %v, want %s", k, e, c[1])
		}
	}
	many := "[" + strings.TrimSuffix(strings.Repeat(`"x",`, 21), ",") + "]"
	if e := CheckValue(p.Uses("c"), []byte(many)); e == nil || e.Code != "plugin_too_large" {
		t.Errorf("21 chips: %v", e)
	}
}
