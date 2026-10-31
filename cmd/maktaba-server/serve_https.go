package main

// Optional browser HTTPS (Epic 35 story 35.16 AC15, design §18.9): with rules.cluster
// tls_web_https on, every machine also serves its web pages on port 8443 with a
// certificate from the home's cluster CA. The API keeps that certificate where this
// supervisor reads it (cluster-web.crt/.key: the home's in the keys directory, a node's
// beside its node.json) while the rule is on, and removes it when the rule is off; this
// supervisor serves 8443 exactly while the files are there.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// httpsAddr is the HTTPS port (ports.WebHTTPS).
var httpsAddr = ports.WebHTTPS.Addr()

// The web certificate's files.
const (
	webCertFile = "cluster-web.crt"
	webKeyFile  = "cluster-web.key"
)

// webCertDirs are where the API leaves the web certificate (the home's keys directory,
// a node's node.json folder).
func webCertDirs() []string {
	return []string{config.KeysDir(), filepath.Dir(config.LocalNodeConfigPath())}
}

// webCert finds the web certificate's pair, nil while there is none.
func webCert(dirs []string) *tls.Certificate {
	for _, d := range dirs {
		pair, err := tls.LoadX509KeyPair(filepath.Join(d, webCertFile), filepath.Join(d, webKeyFile))
		if err == nil {
			return &pair
		}
	}
	return nil
}

// serveHTTPS runs the web pages on 8443 while the API keeps a web certificate, checked
// every 30 s, until ctx ends.
func serveHTTPS(ctx context.Context, h http.Handler, dirs []string, addr string, every time.Duration) {
	var mu sync.Mutex
	var cur *tls.Certificate
	var srv *http.Server
	stop := func() {
		if srv != nil {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(sctx)
			cancel()
			srv = nil
			fmt.Fprintln(os.Stderr, "[serve] HTTPS on 8443 stopped (tls_web_https off)")
		}
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		pair := webCert(dirs)
		mu.Lock()
		cur = pair
		mu.Unlock()
		switch {
		case pair == nil:
			stop()
		case srv == nil:
			cfg := &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				mu.Lock()
				defer mu.Unlock()
				if cur == nil {
					return nil, errors.New("no web certificate")
				}
				return cur, nil
			}}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[serve] HTTPS cannot listen on %s: %v\n", addr, err)
				break
			}
			srv = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
			go func(s *http.Server, l net.Listener) {
				if err := s.Serve(tls.NewListener(l, cfg)); err != nil && !errors.Is(err, http.ErrServerClosed) {
					fmt.Fprintf(os.Stderr, "[serve] HTTPS server error: %v\n", err)
				}
			}(srv, ln)
			fmt.Fprintf(os.Stderr, "[serve] web UI over HTTPS on %s (cluster CA certificate)\n", addr)
		}
		select {
		case <-ctx.Done():
			stop()
			return
		case <-t.C:
		}
	}
}
