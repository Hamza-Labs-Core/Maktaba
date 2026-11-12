package httpx

import (
	"net/http"
	"testing"
)

func TestPassType(t *testing.T) {
	for in, want := range map[string]string{
		"application/vnd.apple.mpegurl": "application/vnd.apple.mpegurl",
		"video/mp2t":                    "video/mp2t",
		"video/mp4":                     "video/mp4",
		"application/problem+json; charset=utf-8": "application/problem+json; charset=utf-8",
		"text/plain; charset=utf-8":               "text/plain; charset=utf-8",
		"application/dash+xml":                    "application/dash+xml",
		"text/html":                               "application/octet-stream",
		"Text/HTML; charset=utf-8":                "application/octet-stream",
		" application/xhtml+xml":                  "application/octet-stream",
		"image/svg+xml":                           "application/octet-stream",
		"text/xml":                                "application/octet-stream",
		"application/xml;q=1":                     "application/octet-stream",
		"":                                        "",
	} {
		if got := PassType(in); got != want {
			t.Errorf("PassType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPassHeaders(t *testing.T) {
	h := http.Header{}
	if got := PassHeaders(h, http.StatusOK); got != "application/octet-stream" || h.Get("X-Content-Type-Options") != "nosniff" || len(h) != 1 {
		t.Fatalf("no type: %q %v", got, h)
	}
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified, http.StatusSwitchingProtocols} {
		if got := PassHeaders(h, status); got != "" {
			t.Fatalf("no type and no body (%d): %q", status, got)
		}
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	if got := PassHeaders(h, http.StatusNotModified); got != "application/octet-stream" {
		t.Fatalf("a page: %q", got)
	}
	h.Set("Content-Type", "video/mp2t")
	if got := PassHeaders(h, http.StatusOK); got != "video/mp2t" {
		t.Fatalf("a stream: %q", got)
	}
}
