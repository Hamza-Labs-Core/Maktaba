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

// A transcode's variant playlist (EVENT, every segment listed) is served
// starting at its first segment, signed as before.
func TestManifestHandler_ServeRenditionIndex_StartsAtTheBeginning(t *testing.T) {
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	sessID := uuid.New()
	v0 := filepath.Join(layout.HLSDir(sessID.String()), "v0")
	_ = os.MkdirAll(v0, 0o755)
	index := "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n" +
		"#EXTINF:6.000000,\nseg-0.ts\n#EXTINF:6.000000,\nseg-1.ts\n"
	if err := os.WriteFile(filepath.Join(v0, "index.m3u8"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &ManifestHandler{Sessions: &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
		sessID: {ID: sessID, Format: session.FormatHLS, Mode: session.ModeTranscode},
	}}, Layout: layout}
	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+sessID.String()+"/v0/index.m3u8?sig=tok", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(body, "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0\n#EXT-X-VERSION:6\n") ||
		!strings.Contains(body, "seg-1.ts?sig=tok\n") {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
}

// fakeTranscode is a session's transcode in one of its states.
type fakeTranscode struct{ active, ended, finished bool }

func (f fakeTranscode) Stop(context.Context) error { return nil }
func (f fakeTranscode) PID() int                   { return 0 }
func (f fakeTranscode) Active() bool               { return f.active }
func (f fakeTranscode) Ended() bool                { return f.ended }
func (f fakeTranscode) Finished() bool             { return f.finished }

// A transcode over before it wrote its playlists (it failed at once, as a session the
// home served a node did): 410, so the player opens the video again or says it cannot,
// not a 404 it waits on, buffering. While it runs, the 404 is "not yet".
func TestManifestHandler_PlaylistNeverWritten(t *testing.T) {
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	for name, c := range map[string]struct {
		tr   fakeTranscode
		code int
	}{
		"running": {fakeTranscode{active: true}, 404},
		"stopped": {fakeTranscode{ended: true}, 410},
	} {
		id := uuid.New()
		store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
			id: {ID: id, Format: session.FormatHLS, Mode: session.ModeTranscode, Transcoder: c.tr},
		}}
		h := &ManifestHandler{Sessions: store, Layout: layout}
		r := chi.NewRouter()
		r.With(injectSubject(id.String())).Get("/stream/{session_id}/manifest.m3u8", h.ServeMaster)
		r.With(injectSubject(id.String())).Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
		for _, p := range []string{"/manifest.m3u8", "/v0/index.m3u8"} {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+id.String()+p, nil))
			if rec.Code != c.code {
				t.Errorf("%s %s: %d %s; want %d", name, p, rec.Code, rec.Body.String(), c.code)
			}
		}
	}
}

// A playlist or MPD reload: activity while the transcode runs (a paused
// player keeps its session), not once it is over; a transcode over short of
// the end (stopped, failed) answers 410 so the player opens the video again,
// never a playlist ending there; a finished one's is served.
func TestManifestHandler_PlaylistReloads(t *testing.T) {
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	for name, c := range map[string]struct {
		tr      fakeTranscode
		code    int
		touched bool
	}{
		"running":  {fakeTranscode{active: true}, 200, true},
		"finished": {fakeTranscode{ended: true, finished: true}, 200, false},
		"stopped":  {fakeTranscode{ended: true}, 410, false},
	} {
		for _, format := range []session.Format{session.FormatHLS, session.FormatDASH} {
			id := uuid.New()
			dir := layout.HLSDir(id.String())
			_ = os.MkdirAll(filepath.Join(dir, "v0"), 0o755)
			_ = os.WriteFile(filepath.Join(dir, "v0", "index.m3u8"), []byte("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\nseg-0.ts\n#EXT-X-ENDLIST\n"), 0o644)
			_ = os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\nv0/index.m3u8\n"), 0o644)
			_ = os.WriteFile(filepath.Join(dir, "manifest.mpd"), []byte(`<MPD type="static"></MPD>`), 0o644)
			store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
				id: {ID: id, Format: format, Mode: session.ModeTranscode, Transcoder: c.tr},
			}}
			h := &ManifestHandler{Sessions: store, Layout: layout}
			r := chi.NewRouter()
			r.With(injectSubject(id.String())).Get("/stream/{session_id}/manifest.m3u8", h.ServeMaster)
			r.With(injectSubject(id.String())).Get("/stream/{session_id}/manifest.mpd", h.ServeMaster)
			r.With(injectSubject(id.String())).Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
			paths := []string{"/manifest.mpd"}
			if format == session.FormatHLS {
				paths = []string{"/manifest.m3u8", "/v0/index.m3u8"}
			}
			for _, p := range paths {
				store.touched = nil
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+id.String()+p, nil))
				if rec.Code != c.code || (store.touched[id] != time.Time{}) != c.touched {
					t.Errorf("%s %s: %d, touched %v; want %d, %v", name, p, rec.Code, store.touched, c.code, c.touched)
				}
			}
		}
	}
}

// A stream opened 20 s into the video: its playlist lists 0–20 s as gaps
// (none longer than the target duration), starts players at 20 s, and keeps
// #EXT-X-ENDLIST only once the transcode is finished (ffmpeg writes it on
// SIGTERM too).
func TestManifestHandler_ServeRenditionIndex_StartedAtAPosition(t *testing.T) {
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	index := "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n" +
		"#EXT-X-INDEPENDENT-SEGMENTS\n#EXTINF:6.000000,\nseg-0.ts\n#EXTINF:4.000000,\nseg-1.ts\n#EXT-X-ENDLIST\n"
	gaps := "#EXTINF:6.000000,\n#EXT-X-GAP\ngap-0.ts\n#EXTINF:6.000000,\n#EXT-X-GAP\ngap-1.ts\n" +
		"#EXTINF:6.000000,\n#EXT-X-GAP\ngap-2.ts\n#EXTINF:2.000000,\n#EXT-X-GAP\ngap-3.ts\n"
	for name, c := range map[string]struct {
		tr      fakeTranscode
		endlist bool
	}{
		"running":  {fakeTranscode{active: true}, false},
		"finished": {fakeTranscode{ended: true, finished: true}, true},
	} {
		id := uuid.New()
		v0 := filepath.Join(layout.HLSDir(id.String()), "v0")
		_ = os.MkdirAll(v0, 0o755)
		_ = os.WriteFile(filepath.Join(v0, "index.m3u8"), []byte(index), 0o644)
		h := &ManifestHandler{Sessions: &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
			id: {ID: id, Format: session.FormatHLS, Mode: session.ModeTranscode, Transcoder: c.tr, StartSec: 20},
		}}, Layout: layout}
		r := chi.NewRouter()
		r.With(injectSubject(id.String())).Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+id.String()+"/v0/index.m3u8", nil))
		want := "#EXTM3U\n#EXT-X-START:TIME-OFFSET=20\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n" +
			"#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-INDEPENDENT-SEGMENTS\n" + gaps +
			"#EXTINF:6.000000,\nseg-0.ts\n#EXTINF:4.000000,\nseg-1.ts\n"
		if c.endlist {
			want += "#EXT-X-ENDLIST\n"
		}
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d\n%s\nwant\n%s", name, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestGapsBeforeAndEndlist(t *testing.T) {
	pl := "#EXTM3U\r\n#EXT-X-TARGETDURATION:4\r\n#EXTINF:4.000000,\r\nseg-0.ts\r\n#EXT-X-ENDLIST\r\n"
	if got := string(gapsBefore([]byte(pl), 0)); got != pl {
		t.Errorf("no start: %q", got)
	}
	want := "#EXTM3U\r\n#EXT-X-TARGETDURATION:4\r\n#EXTINF:4.000000,\r\n#EXT-X-GAP\r\ngap-0.ts\r\n" +
		"#EXTINF:1.000000,\r\n#EXT-X-GAP\r\ngap-1.ts\r\n#EXTINF:4.000000,\r\nseg-0.ts\r\n#EXT-X-ENDLIST\r\n"
	if got := string(gapsBefore([]byte(pl), 5)); got != want {
		t.Errorf("5 s in, 4 s target: %q", got)
	}
	if got := string(gapsBefore([]byte("#EXTM3U\n"), 5)); got != "#EXTM3U\n" {
		t.Errorf("no segment yet: %q", got)
	}
	if got := string(withoutEndlist([]byte(pl))); got != "#EXTM3U\r\n#EXT-X-TARGETDURATION:4\r\n#EXTINF:4.000000,\r\nseg-0.ts\r\n" {
		t.Errorf("without ENDLIST: %q", got)
	}
	if got := string(startAt([]byte("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n"), 1213)); got != "#EXTM3U\n#EXT-X-START:TIME-OFFSET=1213\n#EXT-X-PLAYLIST-TYPE:EVENT\n" {
		t.Errorf("start at 1213: %q", got)
	}
}

// startAt: only an EVENT playlist without a start of its own, in its
// line endings.
func TestStartAtBeginning(t *testing.T) {
	const start = "#EXT-X-START:TIME-OFFSET=0\n"
	for name, c := range map[string]struct{ in, want string }{
		"event": {"#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\nseg-0.ts\n", "#EXTM3U\n" + start + "#EXT-X-PLAYLIST-TYPE:EVENT\nseg-0.ts\n"},
		"crlf": {"#EXTM3U\r\n#EXT-X-PLAYLIST-TYPE:EVENT\r\n",
			"#EXTM3U\r\n#EXT-X-START:TIME-OFFSET=0\r\n#EXT-X-PLAYLIST-TYPE:EVENT\r\n"},
		"ended": {"#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\nseg-0.ts\n#EXT-X-ENDLIST\n",
			"#EXTM3U\n" + start + "#EXT-X-PLAYLIST-TYPE:EVENT\nseg-0.ts\n#EXT-X-ENDLIST\n"},
		"has one": {"#EXTM3U\n#EXT-X-START:TIME-OFFSET=10\n#EXT-X-PLAYLIST-TYPE:EVENT\n",
			"#EXTM3U\n#EXT-X-START:TIME-OFFSET=10\n#EXT-X-PLAYLIST-TYPE:EVENT\n"},
		"sliding":  {"#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:4\nseg-4.ts\n", "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:4\nseg-4.ts\n"},
		"not hls":  {"<MPD/>", "<MPD/>"},
		"one line": {"#EXTM3U #EXT-X-PLAYLIST-TYPE:EVENT", "#EXTM3U #EXT-X-PLAYLIST-TYPE:EVENT"},
	} {
		if got := string(startAt([]byte(c.in), 0)); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
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

// A player asks for the rendition playlist within milliseconds of the session, before the
// transcode wrote it: it is waited for (Safari's own HLS gave up on the 404, the video
// "errored" though its playlist was a second away). A transcode not running is not
// waited on.
func TestManifestHandler_RenditionWaitedForWhileTheTranscodeStarts(t *testing.T) {
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	ask := func(tr fakeTranscode, write time.Duration) (int, time.Duration) {
		sessID := uuid.New()
		rendDir := filepath.Join(layout.HLSDir(sessID.String()), "v0")
		_ = os.MkdirAll(rendDir, 0o755)
		store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{
			sessID: {ID: sessID, Format: session.FormatHLS, Transcoder: tr},
		}}
		h := &ManifestHandler{Sessions: store, Layout: layout, FirstWrite: 300 * time.Millisecond}
		r := chi.NewRouter()
		r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
		if write > 0 {
			go func() {
				time.Sleep(write)
				_ = os.WriteFile(filepath.Join(rendDir, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:2,\nseg-0.ts\n"), 0o644)
			}()
		}
		start := time.Now()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+sessID.String()+"/v0/index.m3u8", nil))
		return rec.Code, time.Since(start)
	}
	if code, _ := ask(fakeTranscode{active: true}, 100*time.Millisecond); code != 200 {
		t.Fatalf("written a moment after the ask, while the transcode runs: %d, want 200", code)
	}
	if code, took := ask(fakeTranscode{}, 100*time.Millisecond); code != 404 || took > 150*time.Millisecond {
		t.Fatalf("no transcode running: %d after %v, want an immediate 404", code, took)
	}
	if code, took := ask(fakeTranscode{active: true}, 0); code != 404 || took < 300*time.Millisecond {
		t.Fatalf("never written: %d after %v, want 404 after the wait", code, took)
	}
}
