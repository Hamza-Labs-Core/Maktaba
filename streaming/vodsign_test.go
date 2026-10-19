package main

// Video on demand end to end inside the streaming role: the gRPC OpenSession the
// API calls and the public mux the player fetches from share one session store,
// and every manifest/segment request is checked against the machine's stream
// signing key (MAKTABA_STREAM_SIGNING_KEY). Real ffmpeg transcodes a generated
// fixture; the manifest, the rendition playlist and two segments play with the
// one signed URL; tampered, expired, foreign and missing signatures are refused.
//
// Regression pins: before this, the API never signed (no URLSigner was wired),
// the streaming role had no key to verify with (no JWKS URL anywhere), and the
// public mux had its own empty session store, so a session opened over gRPC was
// never found there — sessions opened, no byte played.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/grpcsrv"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// writeStreamKey writes a PKCS#8 PEM RSA key the way the API creates
// stream-signing.key and returns the private key + the kid streaming derives.
func writeStreamKey(t *testing.T, path string) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	kid, _, err := auth.ReadKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return priv, kid
}

func makeFixture(t *testing.T, path string, seconds int) {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=25", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", strconv.Itoa(seconds), "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %v\n%s", err, out)
	}
}

func get(t *testing.T, h http.Handler, path string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, b
}

// childURIs are a playlist's non-comment lines.
func childURIs(b []byte) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func TestVODSignedPlaybackThroughSharedStore(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "stream-signing.key")
	priv, kid := writeStreamKey(t, keyPath)
	t.Setenv("MAKTABA_STREAM_SIGNING_KEY", keyPath)
	t.Setenv("MAKTABA_JWKS_URL", "")
	t.Setenv("MAKTABA_CACHE_ROOT", filepath.Join(tmp, "cache"))

	media := filepath.Join(tmp, "lecture.mp4")
	makeFixture(t, media, 14)

	lib, vid, usr := uuid.New(), uuid.New(), uuid.New()
	backend := probe.NewFakeBackend()
	backend.Set(&probe.Row{VideoID: vid, LibraryID: lib, Path: media, Container: "mp4", VideoCodec: "h264",
		AudioCodec: "aac", Height: 360, Width: 640, BitrateKbps: 1000, Probed: true, ContentHash: "fixture"})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewMemoryStore(time.Second)
	grpcSrv := buildGRPCServer(logger, backend, sessions)
	bg := newBackground(context.Background())
	t.Cleanup(func() { bg.Stop(time.Second) })
	public := buildPublicHandler(bg, logger, backend, nil, nil, sessions)
	t.Cleanup(func() {
		ids, _ := sessions.List(context.Background())
		for _, r := range ids {
			if r.Transcoder != nil {
				_ = r.Transcoder.Stop(context.Background())
			}
		}
	})

	resp, err := grpcSrv.OpenSession(context.Background(), grpcsrv.OpenSessionRequest{
		VideoID: vid.String(), UserID: usr.String(), ForceTranscode: true,
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if resp.Mode != "transcode" || !strings.HasSuffix(resp.ManifestPath, "/manifest.m3u8") {
		t.Fatalf("session %+v", resp)
	}

	sign := func(c auth.Claims) string {
		if c.Exp == 0 {
			c.Exp = time.Now().Add(time.Hour).Unix()
		}
		tok, err := auth.Sign(&c, priv, kid)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	good := sign(auth.Claims{Aud: "streaming", Sub: resp.SessionID, Usr: usr.String(), Lib: []string{lib.String()}})

	code, master := get(t, public, resp.ManifestPath+"?sig="+good)
	if code != 200 {
		t.Fatalf("manifest with the signed URL: %d %s (the gRPC session must be visible to the public mux)", code, master)
	}
	kids := childURIs(master)
	if len(kids) == 0 || !strings.Contains(kids[0], "sig=") {
		t.Fatalf("the master playlist's child URIs must carry the signature: %s", master)
	}
	base := strings.TrimSuffix(resp.ManifestPath, "manifest.m3u8")
	rendition := base + kids[0]

	var segs []string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		code, idx := get(t, public, rendition)
		if code == 200 {
			segs = childURIs(idx)
			if len(segs) >= 2 {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if len(segs) < 2 {
		var files []string
		_ = filepath.Walk(filepath.Join(tmp, "cache"), func(p string, _ os.FileInfo, _ error) error {
			files = append(files, p)
			return nil
		})
		stderr := ""
		if row, ok, _ := sessions.Get(context.Background(), uuid.MustParse(resp.SessionID)); ok {
			if h, ok := row.Transcoder.(*ffmpeg.Handle); ok {
				stderr = h.Stderr()
			}
		}
		t.Fatalf("the rendition playlist %s never listed two segments (%v); cache: %v; ffmpeg: %s", rendition, segs, files, stderr)
	}
	renditionDir := rendition[:strings.LastIndex(rendition, "/")+1]
	for _, s := range segs[:2] {
		if !strings.Contains(s, "sig=") {
			t.Fatalf("segment URI without the signature: %s", s)
		}
		code, b := get(t, public, renditionDir+s)
		if code != 200 || len(b) < 188 || b[0] != 0x47 {
			t.Fatalf("segment %s: %d, %d bytes", s, code, len(b))
		}
		t.Logf("segment %s: %d bytes of MPEG-TS", strings.SplitN(s, "?", 2)[0], len(b))
	}

	// refused: no signature, a tampered one, an expired one, another session's,
	// the wrong audience, another library, a key this machine does not hold
	firstSeg := renditionDir + strings.SplitN(segs[0], "?", 2)[0]
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	foreign, _ := auth.Sign(&auth.Claims{Aud: "streaming", Sub: resp.SessionID, Lib: []string{lib.String()},
		Exp: time.Now().Add(time.Hour).Unix()}, other, kid)
	flip := "AAAA"
	if strings.HasSuffix(good, flip) {
		flip = "BBBB"
	}
	tampered := good[:len(good)-4] + flip
	for name, path := range map[string]string{
		"no signature":      resp.ManifestPath,
		"segment unsigned":  firstSeg,
		"tampered":          resp.ManifestPath + "?sig=" + tampered,
		"tampered segment":  firstSeg + "?sig=" + tampered,
		"expired":           resp.ManifestPath + "?sig=" + sign(auth.Claims{Aud: "streaming", Sub: resp.SessionID, Lib: []string{lib.String()}, Exp: time.Now().Add(-10 * time.Minute).Unix()}),
		"another session":   resp.ManifestPath + "?sig=" + sign(auth.Claims{Aud: "streaming", Sub: uuid.NewString(), Lib: []string{lib.String()}}),
		"direct audience":   resp.ManifestPath + "?sig=" + sign(auth.Claims{Aud: "streaming-direct", Sub: resp.SessionID, Lib: []string{lib.String()}}),
		"another library":   resp.ManifestPath + "?sig=" + sign(auth.Claims{Aud: "streaming", Sub: resp.SessionID, Lib: []string{uuid.NewString()}}),
		"another machine's": resp.ManifestPath + "?sig=" + foreign,
	} {
		if code, b := get(t, public, path); code < 400 || code >= 500 || code == 404 {
			t.Errorf("%s: %d %s, want a refusal", name, code, b)
		}
	}
}
