package main

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// Epic 53 story 53.09: `maktaba-server logs` answers its health and search on loopback,
// with nothing indexed yet, and stops with its context.
func TestLogsRoleServes(t *testing.T) {
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
