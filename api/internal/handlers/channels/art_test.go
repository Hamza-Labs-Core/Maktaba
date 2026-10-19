package channels

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// A tiny but real animated GIF header (two frames are not needed: the
// bytes must only round-trip untouched).
var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR fake png body")
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00NETSCAPE2.0 animated frames")
	webpBytes = []byte("RIFF\x10\x00\x00\x00WEBPVP8 fake")
	jpgBytes  = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F'}
	svgBytes  = []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"></svg>`)
)

type artEnv struct {
	db      *sql.DB
	srv     http.Handler
	dataDir string
}

func newArtEnv(t *testing.T) artEnv {
	t.Helper()
	db, _ := newLogoServer(t)
	for _, ddl := range []string{
		`CREATE TABLE external_links (target TEXT NOT NULL, subject_kind TEXT NOT NULL,
			subject_id TEXT NOT NULL, external_id TEXT NOT NULL, title TEXT NOT NULL,
			content_hash TEXT NOT NULL, created_by_tool INTEGER NOT NULL,
			updated_at TEXT NOT NULL DEFAULT '', PRIMARY KEY (target, subject_kind, subject_id))`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	r := chi.NewRouter()
	(&Handler{DB: db, DataDir: dir}).Mount(r)
	return artEnv{db: db, srv: r, dataDir: dir}
}

var artAdmin = &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}

func (e artEnv) do(t *testing.T, p *principal.Principal, req *http.Request) (*httptest.ResponseRecorder, Channel) {
	t.Helper()
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	var c Channel
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	return rec, c
}

func uploadReq(t *testing.T, id, kind, name string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/channels/"+id+"/art/"+kind, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func (e artEnv) upload(t *testing.T, id, kind, name string, data []byte) (*httptest.ResponseRecorder, Channel) {
	t.Helper()
	return e.do(t, artAdmin, uploadReq(t, id, kind, name, data))
}

func (e artEnv) overrideCount(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM channel_art_overrides WHERE channel_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e artEnv) look(t *testing.T, id string) map[string]any {
	t.Helper()
	var mc string
	if err := e.db.QueryRow(`SELECT mode_config FROM channels WHERE id = $1`, id).Scan(&mc); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	_ = json.Unmarshal([]byte(mc), &m)
	l, _ := m["look"].(map[string]any)
	return l
}

func artFiles(t *testing.T, dir, id string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, ArtDirName, id, "*"))
	return m
}

func TestArtUploadLogo(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	rec, c := e.upload(t, "c1", "logo", "logo.png", pngBytes)
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	if c.LogoPath == nil || !strings.HasPrefix(*c.LogoPath, filepath.Join(e.dataDir, ArtDirName, "c1", "logo-")) ||
		!strings.HasSuffix(*c.LogoPath, ".png") {
		t.Fatalf("logo_path %v", c.LogoPath)
	}
	if c.Art == nil || !c.Art.Logo.Overridden || !strings.HasPrefix(c.Art.Logo.URL, "/api/channels/c1/art/logo?v=") {
		t.Fatalf("art %+v", c.Art)
	}
	got, _ := os.ReadFile(*c.LogoPath)
	if !bytes.Equal(got, pngBytes) {
		t.Fatal("stored bytes differ")
	}
	for _, p := range []string{"/api/channels/c1/art/logo", "/api/channels/c1/logo"} {
		rec, _ := e.do(t, nil, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), pngBytes) {
			t.Fatalf("GET %s: %d %q", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestArtUploadAnimatedGifWatermarkKeptAsIs(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	rec, c := e.upload(t, "c1", "watermark", "corner.gif", gifBytes)
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	wm, _ := e.look(t, "c1")["watermark"].(map[string]any)
	p, _ := wm["path"].(string)
	if !strings.HasSuffix(p, ".gif") || wm["position"] != "top-right" || wm["width_pct"] != 12.0 || wm["margin_pct"] != 2.0 {
		t.Fatalf("look.watermark %v", wm)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, gifBytes) {
		t.Fatal("gif re-encoded")
	}
	if !c.Art.Watermark.Overridden || c.Art.Watermark.Path != p || c.Art.Watermark.Position != "top-right" {
		t.Fatalf("art %+v", c.Art.Watermark)
	}
	rec, _ = e.do(t, nil, httptest.NewRequest("GET", "/api/channels/c1/art/watermark", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("GET watermark %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	// loading screen: webp and jpeg are fine too
	if rec, _ := e.upload(t, "c1", "loading", "x.webp", webpBytes); rec.Code != 200 {
		t.Fatalf("webp %d", rec.Code)
	}
	if rec, c := e.upload(t, "c1", "loading", "x.jpeg", jpgBytes); rec.Code != 200 || !strings.HasSuffix(c.Art.Loading.Path, ".jpg") {
		t.Fatalf("jpg %d %+v", rec.Code, c.Art)
	}
	if l := e.look(t, "c1"); !strings.HasSuffix(l["loading_path"].(string), ".jpg") || l["watermark"] == nil {
		t.Fatalf("look %v", l)
	}
}

func TestArtRefusesByMagicBytes(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	if rec, _ := e.upload(t, "c1", "logo", "evil.png", []byte("#!/bin/sh\necho not an image")); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text as png: %d", rec.Code)
	}
	if rec, _ := e.upload(t, "c1", "watermark", "w.svg", svgBytes); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("svg watermark: %d", rec.Code)
	}
	if rec, _ := e.upload(t, "c1", "loading", "l.svg", svgBytes); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("svg loading: %d", rec.Code)
	}
	// a PNG named .jpg is stored as what it is
	rec, c := e.upload(t, "c1", "logo", "named.jpg", pngBytes)
	if rec.Code != 200 || !strings.HasSuffix(*c.LogoPath, ".png") {
		t.Fatalf("png named jpg: %d %v", rec.Code, c.LogoPath)
	}
	rec, c = e.upload(t, "c1", "logo", "logo.svg", svgBytes)
	if rec.Code != 200 || !strings.HasSuffix(*c.LogoPath, ".svg") {
		t.Fatalf("svg logo: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := e.upload(t, "c1", "banner", "b.png", pngBytes); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown kind: %d", rec.Code)
	}
	if n := len(artFiles(t, e.dataDir, "c1")); n != 1 {
		t.Fatalf("files left: %d", n)
	}
}

func TestArtSizeLimit(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	big := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, ArtMaxBytes)...)
	if rec, _ := e.upload(t, "c1", "logo", "big.png", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("big: %d %s", rec.Code, rec.Body.String())
	}
	exact := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, ArtMaxBytes-len(pngBytes))...)
	if rec, _ := e.upload(t, "c1", "logo", "max.png", exact); rec.Code != 200 {
		t.Fatalf("5 MB exactly: %d", rec.Code)
	}
}

func TestArtAdminOnly(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	viewer := &principal.Principal{UserID: "v", AccessAllLibraries: true}
	if rec, _ := e.do(t, viewer, uploadReq(t, "c1", "logo", "a.png", pngBytes)); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer upload: %d", rec.Code)
	}
	if rec, _ := e.do(t, viewer, httptest.NewRequest("DELETE", "/api/channels/c1/art/logo", nil)); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer delete: %d", rec.Code)
	}
}

func TestArtReplaceRemovesPreviousFile(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	_, first := e.upload(t, "c1", "logo", "a.png", pngBytes)
	_, second := e.upload(t, "c1", "logo", "b.gif", gifBytes)
	if *first.LogoPath == *second.LogoPath {
		t.Fatal("same path for different images")
	}
	if _, err := os.Stat(*first.LogoPath); !os.IsNotExist(err) {
		t.Fatalf("previous file kept: %v", err)
	}
	if first.Art.Logo.URL == second.Art.Logo.URL {
		t.Fatal("art URL not versioned")
	}
	// the watermark's file is not touched by a logo upload
	e.upload(t, "c1", "watermark", "w.png", pngBytes)
	e.upload(t, "c1", "logo", "c.png", append([]byte{}, append(pngBytes, 'x')...))
	if n := len(artFiles(t, e.dataDir, "c1")); n != 2 {
		t.Fatalf("files: %v", artFiles(t, e.dataDir, "c1"))
	}
}

// planChannel makes c1 a channel the lineup plan writer owns, with the
// given plan look.
func planChannel(t *testing.T, e artEnv, logo string, look string) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO external_links (target, subject_kind, subject_id, external_id, title, content_hash, created_by_tool)
		VALUES ('maktaba_channel','catalog','kids','c1','Kids','h',1)`); err != nil {
		t.Fatal(err)
	}
	addPlan(t, e, logo, look)
}

func addPlan(t *testing.T, e artEnv, logo, look string) {
	t.Helper()
	plan := `{"channels":[{"key":"kids","maktaba":{"name":"Kids","number":1,"logo_path":"` + logo +
		`","mode":"schedule","mode_config":{"slots":[],"look":` + look + `}}}]}`
	if _, err := e.db.Exec(`INSERT INTO channel_plans (input_hash, plan) VALUES ('x', $1)`, plan); err != nil {
		t.Fatal(err)
	}
}

func TestArtRevertGoesBackToPlan(t *testing.T) {
	e := newArtEnv(t)
	planLogo := filepath.Join(t.TempDir(), "plan-logo.png")
	planWM := filepath.Join(t.TempDir(), "plan-wm.png")
	insertChannel(t, e.db, "c1", planLogo)
	planLook := `{"watermark":{"path":"` + filepath.ToSlash(planWM) + `","position":"bottom-left","width_pct":10,"margin_pct":1},"loading_path":"/plan/loading.png"}`
	if _, err := e.db.Exec(`UPDATE channels SET mode_config = $1`, `{"slots":[],"look":`+planLook+`}`); err != nil {
		t.Fatal(err)
	}
	planChannel(t, e, filepath.ToSlash(planLogo), planLook)

	_, c := e.upload(t, "c1", "logo", "a.png", pngBytes)
	uploaded := *c.LogoPath
	_, c = e.upload(t, "c1", "watermark", "w.gif", gifBytes)
	if c.Art.Watermark.Position != "bottom-left" || c.Art.Watermark.WidthPct != 10 {
		t.Fatalf("upload must keep the plan's corner: %+v", c.Art.Watermark)
	}
	if e.overrideCount(t, "c1") != 2 {
		t.Fatal("override rows")
	}

	// A new plan arrives meanwhile: the revert follows the latest plan.
	newPlanLogo := filepath.ToSlash(filepath.Join(t.TempDir(), "plan-logo-2.png"))
	addPlan(t, e, newPlanLogo, planLook)

	rec, c := e.do(t, artAdmin, httptest.NewRequest("DELETE", "/api/channels/c1/art/logo", nil))
	if rec.Code != 200 || c.LogoPath == nil || *c.LogoPath != newPlanLogo || c.Art.Logo.Overridden {
		t.Fatalf("revert logo: %d %+v", rec.Code, c)
	}
	if _, err := os.Stat(uploaded); !os.IsNotExist(err) {
		t.Fatal("uploaded logo not removed on revert")
	}
	rec, c = e.do(t, artAdmin, httptest.NewRequest("DELETE", "/api/channels/c1/art/watermark", nil))
	if rec.Code != 200 || c.Art.Watermark.Overridden || c.Art.Watermark.Path != filepath.ToSlash(planWM) {
		t.Fatalf("revert watermark: %d %+v", rec.Code, c.Art.Watermark)
	}
	if e.overrideCount(t, "c1") != 0 || len(artFiles(t, e.dataDir, "c1")) != 0 {
		t.Fatal("overrides/files left after revert")
	}
}

func TestArtRevertWithoutPlanRestoresPrevious(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", "https://cdn.example/old.png")
	e.upload(t, "c1", "logo", "a.png", pngBytes)
	e.upload(t, "c1", "logo", "b.png", append(append([]byte{}, pngBytes...), 'b'))
	_, c := e.do(t, artAdmin, httptest.NewRequest("DELETE", "/api/channels/c1/art/logo", nil))
	if c.LogoPath == nil || *c.LogoPath != "https://cdn.example/old.png" {
		t.Fatalf("revert: %+v", c.LogoPath)
	}
	// nothing set by hand any more: a revert of an untouched kind is a no-op clear
	_, c = e.do(t, artAdmin, httptest.NewRequest("DELETE", "/api/channels/c1/art/loading", nil))
	if c.Art.Loading.Path != "" {
		t.Fatalf("loading: %+v", c.Art.Loading)
	}
}

func TestWatermarkSettings(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	patch := func(body string) (*httptest.ResponseRecorder, Channel) {
		req := httptest.NewRequest("PATCH", "/api/channels/c1/art/watermark", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return e.do(t, artAdmin, req)
	}
	for _, bad := range []string{`{"position":"middle"}`, `{"width_pct":0}`, `{"width_pct":80}`, `{"margin_pct":-1}`} {
		if rec, _ := patch(bad); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
	rec, c := patch(`{"position":"bottom-right","width_pct":20}`)
	if rec.Code != 200 || c.Art.Watermark.Position != "bottom-right" || c.Art.Watermark.WidthPct != 20 || !c.Art.Watermark.Overridden {
		t.Fatalf("settings: %d %+v", rec.Code, c.Art.Watermark)
	}
	// a later upload keeps the chosen corner; a later PATCH of one field keeps the others
	_, c = e.upload(t, "c1", "watermark", "w.png", pngBytes)
	_, c = patch(`{"margin_pct":5}`)
	wm := e.look(t, "c1")["watermark"].(map[string]any)
	if wm["position"] != "bottom-right" || wm["width_pct"] != 20.0 || wm["margin_pct"] != 5.0 || wm["path"] != c.Art.Watermark.Path {
		t.Fatalf("watermark %v", wm)
	}
	if rec, _ := e.do(t, artAdmin, func() *http.Request {
		r := httptest.NewRequest("PATCH", "/api/channels/c1/art/logo", strings.NewReader(`{"position":"top-left"}`))
		r.Header.Set("Content-Type", "application/json")
		return r
	}()); rec.Code != http.StatusNotFound {
		t.Fatalf("logo settings: %d", rec.Code)
	}
}

// The channel form saves the whole mode_config it loaded; an upload done
// since must survive that save, and a plain logo_path save of the same
// value must not turn into a hand override.
func TestPatchKeepsArtOverrides(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	planChannel(t, e, "", `{}`)
	_, up := e.upload(t, "c1", "loading", "l.png", pngBytes)
	body := `{"name":"Kids TV","mode_config":{"filter":{"genre":"kids"}},"logo_path":""}`
	req := httptest.NewRequest("PATCH", "/api/channels/c1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec, c := e.do(t, artAdmin, req)
	if rec.Code != 200 {
		t.Fatalf("patch %d: %s", rec.Code, rec.Body.String())
	}
	if c.Art.Loading.Path != up.Art.Loading.Path || !c.Art.Loading.Overridden {
		t.Fatalf("loading lost: %+v", c.Art.Loading)
	}
	if l := e.look(t, "c1"); l["loading_path"] != up.Art.Loading.Path {
		t.Fatalf("look %v", l)
	}
	if c.Art.Logo.Overridden || e.overrideCount(t, "c1") != 1 {
		t.Fatal("unchanged logo became an override")
	}
	// a typed logo path is a hand override too
	req = httptest.NewRequest("PATCH", "/api/channels/c1", strings.NewReader(`{"logo_path":"https://cdn.example/k.png"}`))
	req.Header.Set("Content-Type", "application/json")
	if _, c = e.do(t, artAdmin, req); !c.Art.Logo.Overridden {
		t.Fatalf("typed logo: %+v", c.Art.Logo)
	}
}

func TestArtSameImageReuploadKeepsTheFileWhenTheSaveFails(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	_, c := e.upload(t, "c1", "logo", "a.png", pngBytes)
	live := *c.LogoPath
	// the save of the second (identical) upload fails
	if _, err := e.db.Exec(`DROP TABLE channel_art_overrides`); err != nil {
		t.Fatal(err)
	}
	if rec, _ := e.upload(t, "c1", "logo", "a.png", pngBytes); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code %d", rec.Code)
	}
	if got, err := os.ReadFile(live); err != nil || !bytes.Equal(got, pngBytes) {
		t.Fatalf("the channel's logo file was removed: %v", err)
	}
}

func TestArtConcurrentUploadsLeaveOneFile(t *testing.T) {
	e := newArtEnv(t)
	insertChannel(t, e.db, "c1", nil)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			img := append(append([]byte{}, pngBytes...), byte('a'+i))
			if rec, _ := e.upload(t, "c1", "logo", "x.png", img); rec.Code != 200 {
				t.Errorf("upload %d: %d %s", i, rec.Code, rec.Body.String())
			}
		}(i)
	}
	wg.Wait()
	files := artFiles(t, e.dataDir, "c1")
	var logo string
	if err := e.db.QueryRow(`SELECT logo_path FROM channels WHERE id = 'c1'`).Scan(&logo); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Clean(files[0]) != filepath.Clean(logo) {
		t.Fatalf("files %v, logo_path %s", files, logo)
	}
}

func TestSniffImage(t *testing.T) {
	cases := map[string][]byte{".png": pngBytes, ".gif": gifBytes, ".webp": webpBytes, ".jpg": jpgBytes, ".svg": svgBytes}
	for want, b := range cases {
		if ext, _, ok := sniffImage(b); !ok || ext != want {
			t.Errorf("%s: got %q %v", want, ext, ok)
		}
	}
	for _, bad := range [][]byte{[]byte("hello"), []byte("RIFF\x00\x00\x00\x00WAVE"), []byte("<html><body>x</body></html>"), {0xFF, 0xD8}} {
		if ext, _, ok := sniffImage(bad); ok {
			t.Errorf("%q sniffed as %s", bad, ext)
		}
	}
}
