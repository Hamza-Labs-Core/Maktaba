package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

type diskOpener struct{}

func (diskOpener) Open(path string) (handlers.FileReader, error) { return os.Open(path) }

// Story 48.4 through the router: a session whose video has sidecars lists them
// in its master; a player follows the rendition's URI to its playlist and the
// playlist's one segment to the sidecar route, every request carrying only
// what the URIs carry (the session's sig). Unsigned, each is refused (401);
// signed for a library the video is not in, refused too (401 wrong-lib, as
// the rest of the session's paths).
func TestServer_SubtitleRenditionsFollowedLikeAPlayer(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	jwks.Set(map[string]*rsa.PublicKey{"k1": &priv.PublicKey})

	media := t.TempDir()
	video := filepath.Join(media, "Film.mkv")
	for name, body := range map[string]string{
		"Film.mkv":    "x",
		"Film.en.vtt": "WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nHello\n",
		"Film.ar.srt": "1\n00:00:01,000 --> 00:00:02,000\nمرحبا\n",
	} {
		if err := os.WriteFile(filepath.Join(media, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	libID := uuid.New()
	row := &probe.Row{VideoID: uuid.New(), LibraryID: libID, Path: video, Container: "mkv",
		VideoCodec: "hevc", AudioCodec: "aac", Height: 1080, BitrateKbps: 8000, DurationSec: 600, Probed: true}
	pb := probe.NewFakeBackend()
	pb.Set(row)
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	store := session.NewMemoryStore(time.Second)
	sess := &session.Row{ID: uuid.New(), VideoID: row.VideoID, UserID: uuid.New(), Mode: session.ModeTranscode,
		Format: session.FormatHLS}
	if err := store.Insert(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	dir := layout.HLSDir(sess.ID.String())
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "master.m3u8"),
		[]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=6000000\nv0/index.m3u8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Deps{Cfg: config.Load(), JWKS: jwks, Probe: probe.NewCache(pb, 16), Profiles: capability.NewRegistry(),
		Sessions: store, Layout: layout, Files: diskOpener{}, Now: time.Now})
	tok := mintToken(t, priv, auth.Claims{Aud: "streaming", Sub: sess.ID.String(), Lib: []string{libID.String()}})
	other := mintToken(t, priv, auth.Claims{Aud: "streaming", Sub: sess.ID.String(), Lib: []string{uuid.New().String()}})

	base, _ := url.Parse("http://home/stream/" + sess.ID.String() + "/manifest.m3u8")
	get := func(u *url.URL) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", u.RequestURI(), nil))
		return rec.Code, rec.Body.String()
	}
	signed := *base
	signed.RawQuery = "sig=" + url.QueryEscape(tok)
	code, master := get(&signed)
	if code != 200 {
		t.Fatalf("master %d %s", code, master)
	}
	uris := regexp.MustCompile(`#EXT-X-MEDIA:TYPE=SUBTITLES,[^\n]*URI="([^"]+)"`).FindAllStringSubmatch(master, -1)
	if len(uris) != 2 || !strings.Contains(master, `LANGUAGE="ar"`) || !strings.Contains(master, `LANGUAGE="en"`) ||
		!strings.Contains(master, `,SUBTITLES="subs"`) {
		t.Fatalf("master:\n%s", master)
	}
	for _, m := range uris {
		pl, _ := base.Parse(m[1])
		code, playlist := get(pl)
		if code != 200 {
			t.Fatalf("%s: %d %s", pl.Path, code, playlist)
		}
		var seg string
		for _, line := range strings.Split(playlist, "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				seg = line
			}
		}
		segURL, _ := pl.Parse(seg)
		if !strings.HasPrefix(segURL.Path, "/stream/"+sess.ID.String()+"/subs/sidecar/") || segURL.Query().Get("sig") != tok {
			t.Fatalf("segment %s", segURL)
		}
		code, vtt := get(segURL)
		if code != 200 || !strings.HasPrefix(vtt, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n") {
			t.Fatalf("%s: %d %s", segURL.Path, code, vtt)
		}
		for _, u := range []*url.URL{pl, segURL} {
			bare := *u
			bare.RawQuery = ""
			if code, _ := get(&bare); code != 401 {
				t.Errorf("%s unsigned: %d", u.Path, code)
			}
			wrong := *u
			wrong.RawQuery = "sig=" + url.QueryEscape(other)
			if code, body := get(&wrong); code != 401 || !strings.Contains(body, "wrong-lib") {
				t.Errorf("%s for another library: %d %s", u.Path, code, body)
			}
		}
	}
	if !strings.Contains(master, "subs-ar/") || !strings.Contains(master, "subs-en/") {
		t.Fatalf("master:\n%s", master)
	}
}

// A session whose video has no sidecar keeps the master on disk as it is.
func TestServer_NoSidecarMasterUnchanged(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	jwks.Set(map[string]*rsa.PublicKey{"k1": &priv.PublicKey})
	media := t.TempDir()
	_ = os.WriteFile(filepath.Join(media, "Film.mkv"), []byte("x"), 0o644)
	libID := uuid.New()
	row := &probe.Row{VideoID: uuid.New(), LibraryID: libID, Path: filepath.Join(media, "Film.mkv"), DurationSec: 600, Probed: true}
	pb := probe.NewFakeBackend()
	pb.Set(row)
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	store := session.NewMemoryStore(time.Second)
	sess := &session.Row{ID: uuid.New(), VideoID: row.VideoID, UserID: uuid.New(), Mode: session.ModeTranscode,
		Format: session.FormatHLS}
	_ = store.Insert(context.Background(), sess)
	dir := layout.HLSDir(sess.ID.String())
	_ = os.MkdirAll(dir, 0o755)
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=6000000\nv0/index.m3u8\n"
	_ = os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte(master), 0o644)
	r := New(Deps{Cfg: config.Load(), JWKS: jwks, Probe: probe.NewCache(pb, 16), Profiles: capability.NewRegistry(),
		Sessions: store, Layout: layout, Files: diskOpener{}, Now: time.Now})
	tok := mintToken(t, priv, auth.Claims{Aud: "streaming", Sub: sess.ID.String(), Lib: []string{libID.String()}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+sess.ID.String()+"/manifest.m3u8?sig="+tok, nil))
	if rec.Code != 200 || rec.Body.String() != string(handlers.SignPlaylist([]byte(master), tok)) {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
}
