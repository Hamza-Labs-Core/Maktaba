package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// fakeSessionStore implements SessionStreamReader.
type fakeSessionStore struct {
	rows    map[uuid.UUID]*session.Row
	touched map[uuid.UUID]time.Time
}

func (f *fakeSessionStore) Get(_ context.Context, id uuid.UUID) (*session.Row, bool, error) {
	r, ok := f.rows[id]
	return r, ok, nil
}
func (f *fakeSessionStore) Touch(_ context.Context, id uuid.UUID, at time.Time) error {
	if f.touched == nil {
		f.touched = map[uuid.UUID]time.Time{}
	}
	f.touched[id] = at
	return nil
}

func TestManifestHandler_ServeMaster(t *testing.T) {
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	sessID := uuid.New()
	hlsDir := layout.HLSDir(sessID.String())
	_ = os.MkdirAll(hlsDir, 0o755)
	master := []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=6000000\nv0/index.m3u8\n")
	if err := os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), master, 0o644); err != nil {
		t.Fatal(err)
	}

	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		sessID: {ID: sessID, Format: session.FormatHLS, Mode: session.ModeTranscode},
	}}
	h := &ManifestHandler{Sessions: store, Layout: layout}

	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/manifest.m3u8", h.ServeMaster)
	req := httptest.NewRequest("GET", "/stream/"+sessID.String()+"/manifest.m3u8", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("ct=%s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cc=%s", rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(rec.Body.String(), "v0/index.m3u8") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestManifestHandler_ServeMaster_FormatMismatch(t *testing.T) {
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	sessID := uuid.New()
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		sessID: {ID: sessID, Format: session.FormatDASH},
	}}
	h := &ManifestHandler{Sessions: store, Layout: layout}
	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/manifest.m3u8", h.ServeMaster)

	req := httptest.NewRequest("GET", "/stream/"+sessID.String()+"/manifest.m3u8", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestManifestHandler_ServeSegment_TouchesSession(t *testing.T) {
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	sessID := uuid.New()
	hlsDir := layout.HLSDir(sessID.String())
	rendDir := filepath.Join(hlsDir, "v0")
	_ = os.MkdirAll(rendDir, 0o755)
	if err := os.WriteFile(filepath.Join(rendDir, "seg-0.ts"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		sessID: {ID: sessID, Format: session.FormatHLS},
	}}
	h := &ManifestHandler{Sessions: store, Layout: layout, Now: func() time.Time { return time.Unix(123, 0) }}

	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/{rendition}/{segment}", h.ServeSegment)
	req := httptest.NewRequest("GET", "/stream/"+sessID.String()+"/v0/seg-0.ts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("cc=%s", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Type") != "video/MP2T" {
		t.Fatalf("ct=%s", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body=%s", rec.Body.String())
	}
	if t1, ok := store.touched[sessID]; !ok || t1.Unix() != 123 {
		t.Fatalf("touch missing or wrong: %v ok=%v", t1, ok)
	}
}

func TestManifestHandler_SegmentMissing404(t *testing.T) {
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	sessID := uuid.New()
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		sessID: {ID: sessID, Format: session.FormatHLS},
	}}
	h := &ManifestHandler{Sessions: store, Layout: layout}
	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/{rendition}/{segment}", h.ServeSegment)
	req := httptest.NewRequest("GET", "/stream/"+sessID.String()+"/v0/seg-99.ts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestBuildMasterPlaylist(t *testing.T) {
	ladder := []ffmpeg.Rendition{
		{Name: "v0", Width: 1920, Height: 1080, BitrateKbps: 6000},
		{Name: "v1", Width: 1280, Height: 720, BitrateKbps: 3000},
	}
	bs := BuildMasterPlaylist(ladder)
	out := string(bs)
	if !strings.HasPrefix(out, "#EXTM3U\n") {
		t.Fatal("missing EXTM3U")
	}
	if !strings.Contains(out, "BANDWIDTH=6000000") {
		t.Fatal("bandwidth wrong")
	}
	if !strings.Contains(out, "v0/index.m3u8") || !strings.Contains(out, "v1/index.m3u8") {
		t.Fatalf("rendition refs missing\n%s", out)
	}
}

// SubtitleHandler tests
type fakeTranscript struct {
	all []TranscriptSegment
}

func (f *fakeTranscript) Stream(_ context.Context, _ string, page, pageSize int) ([]TranscriptSegment, error) {
	start := page * pageSize
	if start >= len(f.all) {
		return nil, nil
	}
	end := start + pageSize
	if end > len(f.all) {
		end = len(f.all)
	}
	return f.all[start:end], nil
}

func TestSubtitleHandler_AutoVTT(t *testing.T) {
	tr := &fakeTranscript{all: []TranscriptSegment{
		{Index: 0, StartSec: 0, EndSec: 2, Text: "Hello world"},
		{Index: 1, StartSec: 2, EndSec: 4, Speaker: "Speaker 1", Text: "<script>alert(1)</script>"},
	}}
	h := &SubtitleHandler{Transcripts: tr}

	r := chi.NewRouter()
	r.Get("/subs/auto.vtt", func(w http.ResponseWriter, req *http.Request) {
		ctx := WithVideoID(req.Context(), "video-1")
		h.ServeAuto(w, req.WithContext(ctx))
	})

	req := httptest.NewRequest("GET", "/subs/auto.vtt", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "WEBVTT") {
		t.Fatalf("body must start with WEBVTT: %q", body[:20])
	}
	if !strings.Contains(body, "Hello world") {
		t.Fatalf("missing first cue: %s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("HTML not escaped: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("expected HTML escape: %s", body)
	}
	if !strings.Contains(body, "<v Speaker 1>") {
		t.Fatalf("speaker tag missing: %s", body)
	}
}

func TestSrtToVtt(t *testing.T) {
	srt := strings.Join([]string{
		"1",
		"00:00:01,500 --> 00:00:03,000",
		"Hello <i>world</i>",
		"",
		"2",
		"00:00:03,000 --> 00:00:05,000",
		"Line two",
		"",
	}, "\n")
	out, err := SrtToVtt(strings.NewReader(srt))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.HasPrefix(got, "WEBVTT") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "00:00:01.500 --> 00:00:03.000") {
		t.Fatalf("comma not converted: %s", got)
	}
	if strings.Contains(got, "<i>") {
		t.Fatalf("HTML not escaped: %s", got)
	}
}

func TestVttTime(t *testing.T) {
	if got := vttTime(0); got != "00:00:00.000" {
		t.Fatalf("0 → %s", got)
	}
	if got := vttTime(3661.5); got != "01:01:01.500" {
		t.Fatalf("3661.5 → %s", got)
	}
}

// ChapterHandler tests
type fakeChapterReader struct{ rows []Chapter }

func (f *fakeChapterReader) List(_ context.Context, _ string) ([]Chapter, error) {
	return f.rows, nil
}

func TestChapterHandler_JSON(t *testing.T) {
	cr := &fakeChapterReader{rows: []Chapter{
		{StartSec: 100, EndSec: 200, Title: "Inferred", Source: "inferred"},
		{StartSec: 100, EndSec: 200, Title: "Embedded", Source: "embedded"},
		{StartSec: 200, EndSec: 300, Title: "Manual", Source: "manual"},
	}}
	h := &ChapterHandler{Reader: cr, Resolve: func(_ context.Context, s string) (string, error) { return s, nil }}

	r := chi.NewRouter()
	sess := uuid.New().String()
	r.With(injectSubject(sess)).Get("/stream/{session_id}/chapters.json", h.ServeJSON)
	req := httptest.NewRequest("GET", "/stream/"+sess+"/chapters.json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var got []Chapter
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d chapters: %+v", len(got), got)
	}
	if got[0].Source != "embedded" {
		t.Fatalf("priority merge failed: %+v", got)
	}
}

func TestMergeChapters_NoOverlap(t *testing.T) {
	out := MergeChapters([]Chapter{
		{StartSec: 0, EndSec: 10, Source: "manual"},
		{StartSec: 20, EndSec: 30, Source: "inferred"},
	})
	if len(out) != 2 {
		t.Fatalf("got %d", len(out))
	}
}

func TestDateRangeTagsForPlaylist(t *testing.T) {
	chs := []Chapter{{Seq: 0, StartSec: 100, EndSec: 200, Title: "Intro"}}
	tags := DateRangeTagsForPlaylist(chs, time.Unix(0, 0).UTC())
	if len(tags) != 1 {
		t.Fatalf("tags=%d", len(tags))
	}
	if !strings.Contains(tags[0], "CLASS=\"chapter\"") {
		t.Fatalf("tag=%s", tags[0])
	}
	if !strings.Contains(tags[0], "X-TITLE=\"Intro\"") {
		t.Fatalf("tag=%s", tags[0])
	}
}

// StaticHandler tests
type fakeResolver struct{ root string }

func (f *fakeResolver) PosterPath(id string) string {
	return filepath.Join(f.root, "posters", id+".jpg")
}
func (f *fakeResolver) SpritePath(id, ext string) string {
	return filepath.Join(f.root, "sprites", id+ext)
}
func (f *fakeResolver) ThumbPath(video, name string) string {
	return filepath.Join(f.root, "thumbs", video, name)
}

func TestStaticHandler_Poster(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "posters"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "posters", "vid.jpg"), []byte("JPEGBYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}

	r := chi.NewRouter()
	r.Get("/stream/posters/{video_id}", h.ServePoster)
	req := httptest.NewRequest("GET", "/stream/posters/vid.jpg", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("ct=%s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "private, max-age=2592000, immutable" {
		t.Fatalf("cc=%s", rec.Header().Get("Cache-Control"))
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("JPEGBYTES")) {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

func TestStaticHandler_PosterMissing404(t *testing.T) {
	dir := t.TempDir()
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/posters/{video_id}", h.ServePoster)
	req := httptest.NewRequest("GET", "/stream/posters/missing.jpg", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status=%d", rec.Code)
	}
}

// Story 23.5 AC-2: a symlink planted inside the session HLS dir that
// points OUTSIDE the cache root must not be followed. Router path
// cleaning cannot defend this — only the canonicalizer can. Result is
// an indistinguishable-from-missing 404.
func TestManifestHandler_ServeSegment_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	secretDir := filepath.Join(filepath.Dir(dir), "secret")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "passwd"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessID := uuid.New()
	hlsDir := layout.HLSDir(sessID.String())
	if err := os.MkdirAll(hlsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink: {hlsDir}/v0 -> {secretDir}. A request for
	// /stream/SID/v0/passwd would, without the canonicalizer, read
	// the secret.
	if err := os.Symlink(secretDir, filepath.Join(hlsDir, "v0")); err != nil {
		t.Fatal(err)
	}
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		sessID: {ID: sessID, Format: session.FormatHLS},
	}}
	h := &ManifestHandler{Sessions: store, Layout: layout}
	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).
		Get("/stream/{session_id}/{rendition}/{segment}", h.ServeSegment)
	req := httptest.NewRequest("GET", "/stream/"+sessID.String()+"/v0/passwd", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("symlink escape status=%d want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("TOPSECRET")) {
		t.Fatal("symlink escape leaked the secret file")
	}
}

// Story 23.5 AC-2: a `..` traversal in the poster video_id must not
// escape the posters dir. Indistinguishable-from-missing 404.
func TestStaticHandler_ServePoster_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "posters"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/posters/*", h.ServePoster)
	// chi splat keeps the encoded traversal out of router cleaning.
	req := httptest.NewRequest("GET", "/stream/posters/..%2Fsecret.txt", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("traversal status=%d want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("TOPSECRET")) {
		t.Fatal("poster traversal leaked the secret file")
	}
}

// Story 23.5 AC-2: a symlink planted in the posters dir that points
// OUTSIDE the cache root must not be followed.
func TestStaticHandler_ServePoster_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	dir := t.TempDir()
	secretDir := filepath.Join(filepath.Dir(dir), "secret")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "passwd"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	postersDir := filepath.Join(dir, "posters")
	if err := os.MkdirAll(postersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// {posters}/leak.jpg -> {secretDir}/passwd
	if err := os.Symlink(filepath.Join(secretDir, "passwd"), filepath.Join(postersDir, "leak.jpg")); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/posters/{video_id}", h.ServePoster)
	req := httptest.NewRequest("GET", "/stream/posters/leak.jpg", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("symlink escape status=%d want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("TOPSECRET")) {
		t.Fatal("poster symlink escape leaked the secret file")
	}
}

// Regression: a legitimate poster still serves with the canonicalizer
// in the path.
func TestStaticHandler_ServePoster_LegitStillServes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "posters"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "posters", "vid.jpg"), []byte("JPEGBYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/posters/{video_id}", h.ServePoster)
	req := httptest.NewRequest("GET", "/stream/posters/vid.jpg", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("legit poster status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("JPEGBYTES")) {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// Story 23.5 AC-2: a `..` traversal in the sprite video_id must not
// escape the sprites dir.
func TestStaticHandler_ServeSprite_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.webp"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sprites"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/sprites/*", h.ServeSprite)
	req := httptest.NewRequest("GET", "/stream/sprites/..%2Fsecret.webp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("traversal status=%d want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("TOPSECRET")) {
		t.Fatal("sprite traversal leaked the secret file")
	}
}

// Story 23.5 AC-2: a symlink planted in the sprites dir that points
// OUTSIDE the cache root must not be followed.
func TestStaticHandler_ServeSprite_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	dir := t.TempDir()
	secretDir := filepath.Join(filepath.Dir(dir), "secret")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "passwd"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	spritesDir := filepath.Join(dir, "sprites")
	if err := os.MkdirAll(spritesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// {sprites}/leak.webp -> {secretDir}/passwd
	if err := os.Symlink(filepath.Join(secretDir, "passwd"), filepath.Join(spritesDir, "leak.webp")); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/sprites/{video_id}", h.ServeSprite)
	req := httptest.NewRequest("GET", "/stream/sprites/leak.webp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("symlink escape status=%d want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("TOPSECRET")) {
		t.Fatal("sprite symlink escape leaked the secret file")
	}
}

// Regression: a legitimate sprite still serves with the canonicalizer
// in the path.
func TestStaticHandler_ServeSprite_LegitStillServes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sprites"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sprites", "vid.webp"), []byte("WEBPBYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/sprites/{video_id}", h.ServeSprite)
	req := httptest.NewRequest("GET", "/stream/sprites/vid.webp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("legit sprite status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("ct=%s", rec.Header().Get("Content-Type"))
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("WEBPBYTES")) {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// Story 23.5 AC-2: a symlinked thumb name must not escape the thumbs
// dir.
func TestStaticHandler_ServeThumb_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	dir := t.TempDir()
	secretDir := filepath.Join(filepath.Dir(dir), "secret")
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "passwd"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	thumbDir := filepath.Join(dir, "thumbs", "vid")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(secretDir, "passwd"), filepath.Join(thumbDir, "leak.jpg")); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/thumbs/{video_id}/{name}", h.ServeThumb)
	req := httptest.NewRequest("GET", "/stream/thumbs/vid/leak.jpg", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("symlink escape status=%d want 404", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("TOPSECRET")) {
		t.Fatal("symlink escape leaked the secret file")
	}
}

// Regression: a legitimate thumb still serves after the canonicalizer
// is in the path.
func TestStaticHandler_ServeThumb_LegitStillServes(t *testing.T) {
	dir := t.TempDir()
	thumbDir := filepath.Join(dir, "thumbs", "vid")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thumbDir, "t1.jpg"), []byte("THUMBJPEG"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/thumbs/{video_id}/{name}", h.ServeThumb)
	req := httptest.NewRequest("GET", "/stream/thumbs/vid/t1.jpg", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("legit thumb status=%d want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("THUMBJPEG")) {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// Quiet unused-import warning for auth — the package is referenced via
// ContextWithClaims in test setups but the helper that wrapped it was
// flagged by `unused`. Use a blank var to keep the import alive.
var _ = auth.ContextWithClaims

// Quiet unused-import warnings for io, errors used by SrtToVtt edge-case tests
var (
	_ = io.EOF
	_ = errors.New
)
