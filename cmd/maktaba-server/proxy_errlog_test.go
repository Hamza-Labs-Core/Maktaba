package main

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// a proxy's failure is logged once per window per distinct error (with the count held
// back), and never for a client that went away.
func TestLogProxyErrorThrottlesAndSkipsCanceled(t *testing.T) {
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	th := newErrThrottle(30 * time.Second)
	th.now = func() time.Time { return now }
	var out bytes.Buffer
	refused := errors.New("dial tcp 127.0.0.1:8081: connect: connection refused")
	r := httptest.NewRequest("GET", "/stream/channel/1/index.m3u8", nil)

	for i := 0; i < 5; i++ {
		logProxyError(&out, th, "stream proxy", r, refused)
	}
	logProxyError(&out, th, "stream proxy", r, errors.New("other"))
	logProxyError(&out, th, "stream proxy", r, context.Canceled)
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	logProxyError(&out, th, "stream proxy", r.WithContext(gone), errors.New("read: the client went away"))
	now = now.Add(31 * time.Second)
	logProxyError(&out, th, "stream proxy", r, refused)

	want := "[serve] level=warn stream proxy: GET /stream/channel/1/index.m3u8: " + refused.Error() + "\n" +
		"[serve] level=warn stream proxy: GET /stream/channel/1/index.m3u8: other\n" +
		"[serve] level=warn stream proxy: GET /stream/channel/1/index.m3u8: " + refused.Error() +
		" (4 more like it in the last 30s)\n"
	if out.String() != want {
		t.Fatalf("log:\n%s\nwant:\n%s", out.String(), want)
	}
	if strings.Contains(out.String(), "canceled") {
		t.Fatal("a client that went away was logged")
	}
}
