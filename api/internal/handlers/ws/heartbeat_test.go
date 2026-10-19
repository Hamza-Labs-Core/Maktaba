package ws

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg/runtimecfgtest"
)

// ws.heartbeat_interval_sec sets the SSE keep-alive interval; unset: 30 s.
func TestHeartbeatIntervalSetting(t *testing.T) {
	db := runtimecfgtest.DB(t)
	h := &Handler{Hub: NewHub(), Settings: runtimecfg.New(db)}
	if got := h.heartbeat(context.Background()); got != DefaultHeartbeat {
		t.Fatalf("default %v", got)
	}
	runtimecfgtest.Set(t, db, "ws.heartbeat_interval_sec", `0.05`)
	h.Settings.Invalidate()
	srv := sseServer(t, h)
	resp, err := http.Get(srv.URL + "/ws/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	pings := make(chan struct{}, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), ": ping") {
				pings <- struct{}{}
			}
		}
	}()
	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-pings:
		case <-deadline:
			t.Fatalf("%d keep-alives within 2 s at a 50 ms interval", i)
		}
	}
}
