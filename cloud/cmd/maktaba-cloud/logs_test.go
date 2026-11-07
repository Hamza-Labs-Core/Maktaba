package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/logship"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
)

// Epic 53 story 53.17 §2: W takes node A's lines and, once indexed, answers a search of them
// with the token, and nothing without it.
func TestWKeepsTheNodesLinesAndAnswersSearches(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- serveLogs(ctx, slog.New(slog.DiscardHandler), dir, addr, "t0k") }()
	base := "http://" + addr
	waitUp(t, base+"/healthz")

	f := &logship.Forwarder{URL: base, Token: "t0k", Node: "A", Service: "cloud.relay", Metrics: obs.New()}
	now := time.Now().UTC()
	for i, lvl := range []string{"info", "warn", "error"} {
		f.Add([]byte(fmt.Sprintf(`{"ts":%q,"level":%q,"msg":"relay_forward_failed","service":"cloud.relay","node":"A","home_h":"0123456789abcdef","i":%d}`,
			now.Add(time.Duration(i)*time.Second).Format(time.RFC3339Nano), lvl, i)))
	}
	if err := f.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	search := func(tok string) (int, string) {
		req, _ := http.NewRequest("GET", base+"/v1/logs/search?q=relay_forward_failed&since="+now.Add(-time.Minute).Format(time.RFC3339), nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, _ := search(""); c != http.StatusUnauthorized {
		t.Fatalf("no token: %d", c)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, body := search("t0k")
		var got struct {
			Lines []map[string]any `json:"lines"`
		}
		_ = json.Unmarshal([]byte(body), &got)
		if c == http.StatusOK && len(got.Lines) == 3 {
			for _, l := range got.Lines {
				if l["node"] != "A" || l["service"] != "cloud.relay" {
					t.Fatalf("a line lost its node or service: %v", l)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not searchable on W: %d %s", c, body)
		}
		time.Sleep(300 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("W did not stop")
	}
}

func waitUp(t *testing.T, u string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(u)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never came up: %v", u, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A W that cannot listen returns its error, and stops the keeper and the index it started.
func TestWThatCannotListenStopsCleanly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() // the address is taken
	done := make(chan error, 1)
	go func() {
		done <- serveLogs(context.Background(), slog.New(slog.DiscardHandler), t.TempDir(), ln.Addr().String(), "t")
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a taken address is an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveLogs hung after its listener failed")
	}
}
