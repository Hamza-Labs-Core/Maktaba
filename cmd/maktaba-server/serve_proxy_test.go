package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestStreamProxyForwardsLiveTVPaths(t *testing.T) {
	var seen []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		_, _ = io.WriteString(w, "ok:"+r.URL.Path)
	}))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)

	mux := http.NewServeMux()
	proxy := newStreamProxy("0.0.0.0:" + u.Port())
	for _, p := range streamProxyPaths {
		mux.Handle(p, proxy)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "spa") })

	for _, path := range []string{"/stream/channel/abc/index.m3u8", "/xmltv.xml", "/lineup.m3u"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || rec.Body.String() != "ok:"+path {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/guide", nil))
	if rec.Body.String() != "spa" {
		t.Errorf("/guide should stay on the SPA, got %q", rec.Body.String())
	}
	if len(seen) != 3 {
		t.Errorf("backend saw %v", seen)
	}
}

func TestStreamProxyDownIsBadGateway(t *testing.T) {
	proxy := newStreamProxy("127.0.0.1:1") // nothing listens
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/xmltv.xml", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code %d", rec.Code)
	}
}
