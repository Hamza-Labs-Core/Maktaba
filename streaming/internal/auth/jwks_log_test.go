package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A refresh of the API's keys that fails is logged once per run of failures (the
// refresh loop retries every few minutes), and the first success after it once.
func TestJWKSRefreshFailuresAreLoggedOncePerRun(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	doc := `{"keys":[{"kty":"RSA","kid":"k1","n":"` + b64(key.N.Bytes()) + `","e":"` +
		b64(big.NewInt(int64(key.E)).Bytes()) + `"}]}`
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(doc))
	}))
	defer srv.Close()
	c, err := NewJWKSCache(context.Background(), srv.URL, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	down.Store(true)
	for range 3 {
		if c.tryRefresh(ctx) == nil {
			t.Fatal("a 502 refresh succeeded")
		}
	}
	down.Store(false)
	if err := c.tryRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.tryRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if n := strings.Count(out, "event=jwks_refresh_failed"); n != 1 {
		t.Errorf("%d failure lines, want 1:\n%s", n, out)
	}
	if n := strings.Count(out, "event=jwks_refresh_recovered"); n != 1 {
		t.Errorf("%d recovery lines, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "jwks: 502") {
		t.Errorf("the failure lacks its error:\n%s", out)
	}
}
