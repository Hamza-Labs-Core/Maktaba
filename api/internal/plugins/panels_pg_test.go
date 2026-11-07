package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 54.20: GET /api/panels and the text rules of the keys a panel shows.

const testPanel = `{"ui":1,"title":"panel.title","blocks":[
	{"type":"kv","key":"loudness","label":"panel.loudness"},
	{"type":"chips","key":"topics"},
	{"type":"markers","key":"silences","label":"panel.silences"},
	{"type":"image"},
	{"type":"text","key":"summary"}]}`

type panelFixture struct {
	*fixture
	h   *Panels
	r   chi.Router
	on  map[string]bool
	pkg string
}

func newPanelFixture(t *testing.T) *panelFixture {
	t.Helper()
	f := newFixture(t)
	var pkg string
	if err := f.db.QueryRow(`SELECT path FROM plugin_installs WHERE plugin_id = $1`, testPlugin).Scan(&pkg); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(pkg, "ui"), 0o755)
	_ = os.WriteFile(filepath.Join(pkg, "ui", "panel.json"), []byte(testPanel), 0o644)
	_ = os.WriteFile(filepath.Join(pkg, "i18n", "en.json"), []byte(`{"plugin.name":"Tagger","panel.title":"Loudness","panel.loudness":"Levels","panel.silences":"Silences"}`), 0o644)
	_ = os.WriteFile(filepath.Join(pkg, "i18n", "ar.json"), []byte(`{"plugin.name":"الواسم","panel.title":"علو الصوت","panel.loudness":"المستويات"}`), 0o644)
	f.exec(t, `UPDATE plugin_installs SET manifest = manifest || '{"strings":{"name":"plugin.name","summary":"s"},"panel":[{"on":"video","schema":"ui/panel.json"}]}'::jsonb,
		digest = 'panels-1' WHERE plugin_id = $1`, testPlugin)
	pf := &panelFixture{fixture: f, on: map[string]bool{testPlugin: true}, pkg: pkg}
	pf.h = &Panels{L: &Lifecycle{DB: f.db, ArtifactsDir: f.s.ArtifactsDir}, On: func(_ context.Context, id string) bool { return pf.on[id] }}
	pf.r = chi.NewRouter()
	pf.h.Mount(pf.r)
	return pf
}

func (f *panelFixture) get(t *testing.T, p *principal.Principal, url string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func (f *panelFixture) panels(t *testing.T, p *principal.Principal, lang string) []PanelView {
	t.Helper()
	code, body := f.get(t, p, "/api/panels?lang="+lang+"&subject=video:"+f.videoA)
	if code != 200 {
		t.Fatalf("GET /api/panels = %d %s", code, body)
	}
	var out struct {
		Panels []PanelView `json:"panels"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out.Panels
}

func (f *panelFixture) put(t *testing.T, tok, key, value string) (int, string) {
	t.Helper()
	code, c, _ := f.call(t, tok, "data/put", map[string]any{"subject": map[string]string{"kind": "video", "id": f.videoA}, "key": key, "value": value})
	return code, c
}

var (
	adminP  = &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}
	viewerB = func(lib string) *principal.Principal {
		return &principal.Principal{UserID: "v", Libraries: []string{lib}}
	}
)

// T03: a URL, HTML or a control character in a key a panel shows is refused; another key is
// the plugin's own data, unchecked.
func TestPanelKeysHoldPlainText(t *testing.T) {
	f := newPanelFixture(t)
	tok := f.mint(t).Token
	for _, v := range []string{`[{"label":"x","value":"https://evil.example/x.png"}]`, `[{"label":"<b>x</b>","value":"1"}]`, `[{"label":"a\u202eb","value":"1"}]`} {
		if code, c := f.put(t, tok, "loudness", v); code != 400 || c != CodeTextForbidden {
			t.Fatalf("%s: %d %s", v, code, c)
		}
	}
	if code, c := f.put(t, tok, "topics", `"not a list"`); code != 400 || c != CodeInvalid {
		t.Fatalf("wrong shape: %d %s", code, c)
	}
	if code, c := f.put(t, tok, "notes", `{"url":"https://example.com"}`); code != 200 {
		t.Fatalf("a key no panel shows: %d %s", code, c)
	}
	if code, c := f.put(t, tok, "loudness", `[{"label":"Integrated","value":"-14 LUFS"}]`); code != 200 {
		t.Fatalf("plain text: %d %s", code, c)
	}
}

// T02, T06: everyone who can see the video sees the panel in their language; nobody signed
// in is 401, a viewer without the library 404; a plugin turned off shows nothing.
func TestPanelsFollowAccessAndPhase(t *testing.T) {
	f := newPanelFixture(t)
	tok := f.mint(t).Token
	f.put(t, tok, "loudness", `[{"label":"Integrated","value":"-14 LUFS"}]`)
	f.put(t, tok, "topics", `["fiqh","الصيام"]`)
	f.put(t, tok, "summary", `"A talk on fasting."`)
	got := f.panels(t, adminP, "en")
	if len(got) != 1 || got[0].Title != "Loudness" || got[0].Plugin != "Tagger" || len(got[0].Blocks) != 3 {
		t.Fatalf("admin: %+v", got)
	}
	if b := got[0].Blocks[0]; b.Type != "kv" || b.Label != "Levels" || !strings.Contains(string(b.Value), "-14 LUFS") {
		t.Fatalf("kv block %+v", b)
	}
	ar := f.panels(t, viewerB(f.libA), "ar")
	if len(ar) != 1 || ar[0].Title != "علو الصوت" || ar[0].Blocks[0].Label != "المستويات" {
		t.Fatalf("viewer, Arabic: %+v", ar)
	}
	if code, _ := f.get(t, nil, "/api/panels?subject=video:"+f.videoA); code != 401 {
		t.Fatalf("nobody: %d", code)
	}
	if code, _ := f.get(t, viewerB(f.libB), "/api/panels?subject=video:"+f.videoA); code != 404 {
		t.Fatalf("viewer without the library: %d", code)
	}
	if code, _ := f.get(t, adminP, "/api/panels?subject=video:00000000-0000-0000-0000-000000000000"); code != 404 {
		t.Fatalf("no such video: %d", code)
	}
	f.on[testPlugin] = false
	if got := f.panels(t, viewerB(f.libA), "en"); len(got) != 0 {
		t.Fatalf("plugin off: %+v", got)
	}
	f.on[testPlugin] = true
	// a subject with no data for the panel's keys has no panel
	if code, body := f.get(t, adminP, "/api/panels?subject=video:"+f.videoB); code != 200 || !strings.Contains(string(body), `"panels":[]`) {
		t.Fatalf("no data: %d %s", code, body)
	}
}

// Markers found in a file that changed since are not shown; a panel image is served to
// those who can see the video, as the image only.
func TestPanelMarkersAndImage(t *testing.T) {
	f := newPanelFixture(t)
	tok := f.mint(t).Token
	f.put(t, tok, "silences", `[{"t_ms":1500,"label":"pause"}]`)
	kinds := func() []string {
		var out []string
		for _, b := range f.panels(t, adminP, "en")[0].Blocks {
			out = append(out, b.Type)
		}
		sort.Strings(out)
		return out
	}
	f.exec(t, `UPDATE videos SET mtime = now() - interval '1 day' WHERE id = $1`, f.videoA)
	if k := kinds(); len(k) != 1 || k[0] != "markers" {
		t.Fatalf("blocks %v", k)
	}
	f.exec(t, `UPDATE videos SET mtime = now() + interval '1 minute' WHERE id = $1`, f.videoA)
	if code, body := f.get(t, adminP, "/api/panels?subject=video:"+f.videoA); code != 200 || strings.Contains(string(body), "markers") {
		t.Fatalf("markers of an older file: %s", body)
	}
	f.grant(t, []string{"artwork.write"}, []string{f.libA})
	tok = f.mint(t).Token
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	code, c, _ := f.call(t, tok, "write/set-artwork", map[string]any{"subject": map[string]string{"kind": "video", "id": f.videoA}, "kind": "panel", "image": png})
	if code != 200 {
		t.Fatalf("panel image: %d %s", code, c)
	}
	big := append(append([]byte{}, png...), make([]byte, 1<<20)...)
	if code, c, _ := f.call(t, tok, "write/set-artwork", map[string]any{"subject": map[string]string{"kind": "video", "id": f.videoA}, "kind": "panel", "image": big}); code != 413 {
		t.Fatalf("a 1 MB panel image: %d %s", code, c)
	}
	if k := kinds(); len(k) != 1 || k[0] != "image" {
		t.Fatalf("blocks with the image %v", k)
	}
	if code, body := f.get(t, viewerB(f.libA), "/api/panels/image?plugin="+testPlugin+"&subject=video:"+f.videoA); code != 200 || len(body) != len(png) {
		t.Fatalf("image: %d", code)
	}
	if code, _ := f.get(t, viewerB(f.libB), "/api/panels/image?plugin="+testPlugin+"&subject=video:"+f.videoA); code != 404 {
		t.Fatalf("image without the library: %d", code)
	}
}

// T07: a panels answer within 30 ms. The median of 40 calls is held to it; the p95 is
// logged, not asserted: on a shared runner (or a loaded Mac) the tail measures the machine,
// not the route, and a test that fails on load is a flake.
func TestPanelsAnswerFast(t *testing.T) {
	f := newPanelFixture(t)
	tok := f.mint(t).Token
	f.put(t, tok, "loudness", `[{"label":"Integrated","value":"-14 LUFS"}]`)
	f.put(t, tok, "summary", `"A talk."`)
	var took []time.Duration
	for i := 0; i < 40; i++ {
		start := time.Now()
		f.panels(t, adminP, "en")
		took = append(took, time.Since(start))
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	t.Logf("median %v, p95 %v", took[19], took[37])
	if took[19] > 30*time.Millisecond {
		t.Fatalf("median %v", took[19])
	}
}

// An install with a panel file that is not ui@1 is refused (manifest_bad_panel).
func TestCheckPanels(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "ui"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "ui", "panel.json"), []byte(`{"ui":1,"title":"t","blocks":[{"type":"script","key":"x"}]}`), 0o644)
	m := json.RawMessage(`{"panel":[{"on":"video","schema":"ui/panel.json"}]}`)
	if err := CheckPanels(dir, m); err == nil {
		t.Fatal("a bad panel passed")
	}
	if err := CheckPanels(dir, json.RawMessage(`{"panel":[{"on":"video","schema":"ui/missing.json"}]}`)); err == nil {
		t.Fatal("a missing panel file passed")
	}
	_ = os.WriteFile(filepath.Join(dir, "ui", "panel.json"), []byte(testPanel), 0o644)
	if err := CheckPanels(dir, m); err != nil {
		t.Fatal(err)
	}
}

// Story 54.20 §1: pages show panels in the admin's order, then install order; the order is
// audited and refuses a plugin named twice.
func TestPanelOrder(t *testing.T) {
	f := newPanelFixture(t)
	tok := f.mint(t).Token
	f.put(t, tok, "summary", `"From the first."`)
	// a second plugin with a panel on videos, installed later, with data for the video
	const second = "dev.example.second"
	f.exec(t, `INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest, installed_at)
		SELECT $1, version, 'panels-2', path, manifest, installed_at + interval '1 minute' FROM plugin_installs WHERE plugin_id = $2`,
		second, testPlugin)
	f.exec(t, `INSERT INTO plugin_data (plugin_id, subject_kind, subject_id, key, value, bytes) VALUES ($1, 'video', $2, 'summary', '"From the second."', 18)`,
		second, f.videoA)
	f.on[second] = true
	ids := func() []string {
		var out []string
		for _, p := range f.panels(t, adminP, "en") {
			out = append(out, p.PluginID)
		}
		return out
	}
	if got := ids(); len(got) != 2 || got[0] != testPlugin {
		t.Fatalf("install order: %v", got)
	}
	l := f.h.L
	if _, err := l.SetPanelOrder(context.Background(), []string{second, testPlugin}, admin); err != nil {
		t.Fatal(err)
	}
	if got := ids(); got[0] != second || got[1] != testPlugin {
		t.Fatalf("the admin's order: %v", got)
	}
	list, order, err := l.PanelPlugins(context.Background())
	if err != nil || len(list) != 2 || list[0].ID != second || len(order) != 2 || list[0].On[0] != "video" {
		t.Fatalf("panel plugins %+v %v %v", list, order, err)
	}
	if _, err := l.SetPanelOrder(context.Background(), []string{second, second}, admin); err == nil {
		t.Fatal("a plugin named twice")
	}
	var n int
	_ = f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'plugin.panel_order'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit rows %d", n)
	}
}

func TestOrderBy(t *testing.T) {
	got := orderBy([]string{"a", "b", "c", "d"}, []string{"c", "gone", "a"})
	if want := "c,a,b,d"; strings.Join(got, ",") != want {
		t.Fatalf("%v, want %s", got, want)
	}
}
