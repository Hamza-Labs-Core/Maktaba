package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// Epic 53 story 53.09: `maktaba-server logs` answers its health and search on loopback,
// with nothing indexed yet, and stops with its context.
func TestLogsRoleServes(t *testing.T) {
	// story 53.12: it scrapes the metrics it is pointed at for the dashboards
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "go_goroutines 42\n")
	}))
	defer src.Close()
	t.Setenv("MAKTABA_METRICS_URLS", src.URL)
	addr := "127.0.0.1:" + itoa(testport.Free(t))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runLogIndex(ctx, t.TempDir(), addr, config.LogsSection{}) }()
	var ok bool
	for i := 0; i < 100 && !ok; i++ {
		if r, err := http.Get("http://" + addr + "/healthz"); err == nil {
			ok = r.StatusCode == 200
			r.Body.Close()
		}
		if !ok {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !ok {
		t.Fatal("the role never answered /healthz")
	}
	r, err := http.Get("http://" + addr + "/search?q=nothing")
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("search: %v %v", r, err)
	}
	r.Body.Close()
	scraped := false
	for i := 0; i < 100 && !scraped; i++ {
		if r, err := http.Get("http://" + addr + "/dashboards/overview?range=1h"); err == nil {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			scraped = strings.Contains(string(b), `"go_goroutines"`)
		}
		if !scraped {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !scraped {
		t.Fatal("the role did not scrape MAKTABA_METRICS_URLS into the overview")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopped with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the role did not stop")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
