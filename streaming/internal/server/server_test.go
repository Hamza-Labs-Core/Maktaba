package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

func mintToken(t *testing.T, priv *rsa.PrivateKey, claims auth.Claims) string {
	t.Helper()
	if claims.Exp == 0 {
		claims.Exp = time.Now().Add(time.Hour).Unix()
	}
	tok, err := auth.Sign(&claims, priv, "k1")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestServer_DirectPlay_HappyPath(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	jwks.Set(map[string]*rsa.PublicKey{"k1": &priv.PublicKey})

	libID := uuid.New()
	row := &probe.Row{
		VideoID: uuid.New(), LibraryID: libID, Path: "/v/x.mp4",
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
		Height: 1080, BitrateKbps: 4000, Probed: true,
	}
	pb := probe.NewFakeBackend()
	pb.Set(row)
	pc := probe.NewCache(pb, 16)

	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	store := session.NewMemoryStore(time.Second)

	cfg := config.Load()
	cfg.JWT.ClockSkewLeewaySec = 60

	r := New(Deps{
		Cfg:      cfg,
		JWKS:     jwks,
		Probe:    pc,
		Profiles: capability.NewRegistry(),
		Sessions: store,
		Layout:   layout,
		Files:    &fakeOpener{file: []byte("BYTES")},
		Now:      time.Now,
	})

	tok := mintToken(t, priv, auth.Claims{
		Aud: "streaming-direct", Sub: row.VideoID.String(),
		Lib: []string{libID.String()},
	})

	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String()+"?sig="+tok, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "BYTES" {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestServer_DirectPlay_WrongLib(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	jwks.Set(map[string]*rsa.PublicKey{"k1": &priv.PublicKey})

	libA, libB := uuid.New(), uuid.New()
	row := &probe.Row{
		VideoID: uuid.New(), LibraryID: libA, Path: "/v/x.mp4",
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
		Height: 1080, BitrateKbps: 4000, Probed: true,
	}
	pb := probe.NewFakeBackend()
	pb.Set(row)
	pc := probe.NewCache(pb, 16)

	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	cfg := config.Load()

	r := New(Deps{
		Cfg: cfg, JWKS: jwks, Probe: pc, Profiles: capability.NewRegistry(),
		Sessions: session.NewMemoryStore(time.Second), Layout: layout,
		Files: &fakeOpener{file: []byte("X")}, Now: time.Now,
	})

	tok := mintToken(t, priv, auth.Claims{
		Aud: "streaming-direct", Sub: row.VideoID.String(),
		Lib: []string{libB.String()},
	})

	req := httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String()+"?sig="+tok, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "wrong-lib") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestServer_NoSig_Returns401(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	jwks.Set(map[string]*rsa.PublicKey{"k1": &priv.PublicKey})
	cfg := config.Load()
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()

	r := New(Deps{
		Cfg: cfg, JWKS: jwks,
		Probe:    probe.NewCache(probe.NewFakeBackend(), 16),
		Profiles: capability.NewRegistry(),
		Sessions: session.NewMemoryStore(time.Second),
		Layout:   layout,
		Files:    &fakeOpener{}, Now: time.Now,
	})

	id := uuid.New()
	req := httptest.NewRequest("GET", "/stream/direct/"+id.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "signed-url-missing") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestServer_Healthz(t *testing.T) {
	cfg := config.Load()
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()

	r := New(Deps{
		Cfg: cfg, JWKS: jwks,
		Probe:    probe.NewCache(probe.NewFakeBackend(), 16),
		Profiles: capability.NewRegistry(),
		Sessions: session.NewMemoryStore(time.Second),
		Layout:   layout, Files: &fakeOpener{},
	})

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestServer_404Problem(t *testing.T) {
	cfg := config.Load()
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()

	r := New(Deps{
		Cfg: cfg, JWKS: jwks,
		Probe:    probe.NewCache(probe.NewFakeBackend(), 16),
		Profiles: capability.NewRegistry(),
		Sessions: session.NewMemoryStore(time.Second),
		Layout:   layout, Files: &fakeOpener{},
	})

	req := httptest.NewRequest("GET", "/no/such/route", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/problem+json; charset=utf-8" {
		t.Fatalf("ct=%s", rec.Header().Get("Content-Type"))
	}
}
