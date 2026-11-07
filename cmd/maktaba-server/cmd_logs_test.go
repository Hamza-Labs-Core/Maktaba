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
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logindex"
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

// Epic 53 stories 53.10, 53.11 and 53.15: the index keeps what [logs] says (logs, error
// groups, problem reports, its share of the cap), and scrapes the API's admin metrics when
// nothing else is named.
func TestConfigureIndexFollowsLogs(t *testing.T) {
	t.Setenv("MAKTABA_METRICS_URLS", "")
	ix, err := logindex.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	configureIndex(ix, config.LogsSection{Retention: "14d", ErrorRetention: "60d", ReportRetention: "120d", MaxTotalSize: "2GB"})
	day := 24 * time.Hour
	if ix.Retention != 14*day || ix.ErrorRetention != 60*day || ix.ReportRetention != 120*day || ix.MaxBytes != (2<<30)*35/100 {
		t.Fatalf("index: %v %v %v %d", ix.Retention, ix.ErrorRetention, ix.ReportRetention, ix.MaxBytes)
	}
	if len(ix.MetricsURLs) != 1 || !strings.HasSuffix(ix.MetricsURLs[0], ":9100/metrics") {
		t.Fatalf("metrics: %v", ix.MetricsURLs)
	}
}
