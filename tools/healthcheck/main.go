// Tiny health probe baked into the api and streaming images so compose
// healthchecks don't depend on curl / wget being present in the
// distroless final stage. As of Story 21.4 the admin port (api 9100,
// streaming 9101) carries /healthz and /readyz; this probe defaults to
// the API admin port and accepts any 2xx response.
//
// Configuration:
//
//	HEALTHCHECK_URL  defaults to http://127.0.0.1:9100/healthz
//	HEALTHCHECK_TIMEOUT  Go duration string (default "2s")
//
// Exit 0 on a 2xx response; non-zero otherwise. Compose's CMD form
// invokes this with no arguments.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	url := os.Getenv("HEALTHCHECK_URL")
	if url == "" {
		url = "http://127.0.0.1:9100/healthz"
	}
	timeout := 2 * time.Second
	if v := os.Getenv("HEALTHCHECK_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(2)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
