package ghasset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTransient(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&StatusError{Code: 500}, true},
		{fmt.Errorf("wrapped: %w", &StatusError{Code: 504}), true},
		{&StatusError{Code: 429}, true},
		{&StatusError{Code: 408}, true},
		{&StatusError{Code: 404}, false},
		{&StatusError{Code: 403}, false},
		{&url.Error{Op: "Get", URL: "x", Err: errors.New("connection refused")}, true},
		{&url.Error{Op: "Get", URL: "x", Err: &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}}, false},
		{io.ErrUnexpectedEOF, true},
		{context.DeadlineExceeded, true},
		{context.Canceled, false},
		{&url.Error{Op: "Get", URL: "x", Err: context.Canceled}, false},
		{fmt.Errorf("x: %w", ErrNoAsset), false},
		{errors.New("disk full"), false},
		{nil, false},
	} {
		if got := Transient(c.err); got != c.want {
			t.Errorf("Transient(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestBackoffDoubles(t *testing.T) {
	if Backoff(time.Second, 1) != time.Second || Backoff(time.Second, 3) != 4*time.Second {
		t.Fatal(Backoff(time.Second, 1), Backoff(time.Second, 3))
	}
}

// The asset route is built from the API base and the asset's id, found by name; the
// answer's own URL is not used.
func TestAssetURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/o/r/releases/tags/v1.2.3" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("credentials sent")
		}
		_, _ = io.WriteString(w, `{"assets":[{"id":7,"name":"a.txt","url":"https://evil.invalid/7"},{"id":9,"name":"b.tar.gz"}]}`)
	}))
	defer srv.Close()
	u, err := AssetURL(context.Background(), srv.Client(), srv.URL+"/", "o/r", "v1.2.3", "b.tar.gz")
	if err != nil || u != srv.URL+"/repos/o/r/releases/assets/9" {
		t.Fatalf("%s %v", u, err)
	}
	if _, err := AssetURL(context.Background(), srv.Client(), srv.URL, "o/r", "v1.2.3", "c"); !errors.Is(err, ErrNoAsset) || Transient(err) {
		t.Fatalf("missing asset: %v", err)
	}
	_, err = AssetURL(context.Background(), srv.Client(), srv.URL, "o/r", "v9", "b.tar.gz")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("missing release: %v", err)
	}
}

func TestAssetRequestAsksForTheFile(t *testing.T) {
	req, err := AssetRequest(context.Background(), "https://api.example/repos/o/r/releases/assets/1")
	if err != nil || req.Header.Get("Accept") != "application/octet-stream" || req.Header.Get("Authorization") != "" {
		t.Fatalf("%v %v", req.Header, err)
	}
}
