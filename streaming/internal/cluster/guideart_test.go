package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/hdhr"
)

// guideSrc: one channel whose logo is in <data>/channel-art, one video on air
// with a thumbnail, one not on air.
type guideSrc struct {
	logo         string
	onAir, other uuid.UUID
	hash         string
}

func (g guideSrc) GuideChannels(context.Context) ([]hdhr.GuideChannel, error) { return nil, nil }
func (g guideSrc) GuidePrograms(context.Context, time.Time, time.Time) ([]hdhr.GuideProgram, error) {
	return nil, nil
}
func (g guideSrc) ChannelLogo(context.Context, uuid.UUID) (string, error) { return g.logo, nil }
func (g guideSrc) PlanArtRoots(context.Context) ([]string, error)         { return nil, nil }
func (g guideSrc) ProgrammeArt(_ context.Context, _, v uuid.UUID, _, _ time.Time) (hdhr.ProgrammeArt, error) {
	return hdhr.ProgrammeArt{OnAir: v == g.onAir, Video: hdhr.ArtVideo{ID: v.String(), ContentHash: g.hash}}, nil
}

// The guide's images through a node: the node's routes fetch them from the
// home's signed asset route; a stranger without the key gets nothing.
func TestGuideImagesThroughANode(t *testing.T) {
	data, thumbs := t.TempDir(), t.TempDir()
	logo := filepath.Join(data, hdhr.ArtDirName, "logo.png")
	_ = os.MkdirAll(filepath.Dir(logo), 0o755)
	_ = os.WriteFile(logo, []byte("LOGO"), 0o644)
	_ = os.MkdirAll(filepath.Join(thumbs, "h1"), 0o755)
	_ = os.WriteFile(filepath.Join(thumbs, "h1", "poster.jpg"), []byte("THUMB"), 0o644)
	src := guideSrc{logo: logo, onAir: uuid.New(), other: uuid.New(), hash: "h1"}
	ch := uuid.New()

	k := NewKeyring([]byte("secret"))
	home := &hdhr.GuideHandler{Repo: src, DataDir: data, ThumbnailRoot: thumbs}
	assets := &AssetHandler{Looks: looks{}, Keyring: k, Guide: home}
	hr := chi.NewRouter()
	assets.Mount(hr)
	home.Mount(hr)
	srv := httptest.NewServer(hr)
	defer srv.Close()

	// a stranger cannot read the signed route
	resp, err := http.Get(srv.URL + "/internal/tv/assets/" + ch.String() + "/guide-logo")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("unsigned asset read")
	}

	node := &hdhr.GuideHandler{Repo: src,
		Remote: &AssetCache{Base: func() string { return srv.URL }, Keyring: k, Dir: t.TempDir()}}
	nr := chi.NewRouter()
	node.MountArt(nr)
	for _, c := range []struct {
		path string
		code int
		body string
	}{
		{"/channel/" + ch.String() + "/logo", 200, "LOGO"},
		{"/tv/art/channel/" + ch.String() + "/video/" + src.onAir.String(), 200, "THUMB"},
		{"/tv/art/channel/" + ch.String() + "/video/" + src.other.String(), 404, ""},
	} {
		for _, r := range []http.Handler{hr, nr} { // the home itself, then the node
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
			if rec.Code != c.code || (c.code == 200 && (rec.Body.String() != c.body ||
				rec.Header().Get("Cache-Control") == "" || rec.Header().Get("Content-Type") == "")) {
				t.Errorf("%s → %d %q %v", c.path, rec.Code, rec.Body.String(), rec.Header())
			}
		}
	}
	// the home's signed route never serves a video that is not on air
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/internal/tv/assets/"+ch.String()+"/guide-video/"+src.other.String(), nil)
	k.SignRequest(req, 0)
	hr.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("signed off-air: %d", rec.Code)
	}
}
