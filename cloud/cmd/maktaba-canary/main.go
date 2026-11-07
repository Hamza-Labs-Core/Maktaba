// Command maktaba-canary is the canary home (Epic 50 story 50.10): a real cloudlink agent, the
// same code every home runs, linked to the cloud account "canary" with the slug "canary", in
// front of a tiny API the outside probes play through the relay:
//
//	GET /api/health        200 {"ok":true,"t":<unix ms>}
//	GET /canary/10mb.bin   10 MB of fixed bytes (throughput, not just reachability)
//
// It runs on the witness W. Env: CANARY_ENDPOINT (wss://relay.maktaba.tv/v1/relay/ws),
// CANARY_SERVER_ID, CANARY_SECRET_FILE (the server secret from the claim, 0600),
// CANARY_LISTEN (127.0.0.1:8099, the stub's loopback address).
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cloudlink"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	listen := env("CANARY_LISTEN", "127.0.0.1:8099")
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatal(err)
	}
	go func() { log.Fatal(http.Serve(ln, Stub())) }()

	secret, err := os.ReadFile(env("CANARY_SECRET_FILE", "/etc/maktaba/canary.secret"))
	if err != nil {
		log.Fatalf("canary: the server secret: %v", err)
	}
	sup := &cloudlink.Supervisor{
		Dialer: &cloudlink.Dialer{
			Endpoint: env("CANARY_ENDPOINT", "wss://relay.maktaba.tv/v1/relay/ws"),
			ServerID: os.Getenv("CANARY_SERVER_ID"),
			Secret:   strings.TrimSpace(string(secret)),
		},
		Proxy: &cloudlink.LocalProxy{BaseURL: "http://" + listen},
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log.Printf("canary: serving %s through %s", listen, sup.Dialer.Endpoint)
	_ = sup.Run(ctx)
}

// tenMB is the canary's fixed file.
var tenMB = func() []byte {
	b := make([]byte, 10<<20)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}()

// Stub is the canary home's API.
func Stub() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"t":` + itoa(time.Now().UnixMilli()) + "}"))
	})
	mux.HandleFunc("/canary/10mb.bin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "10mb.bin", time.Unix(0, 0), strings.NewReader(string(tenMB)))
	})
	return mux
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
