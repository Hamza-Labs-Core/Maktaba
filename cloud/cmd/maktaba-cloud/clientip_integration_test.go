//go:build integration

package main

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
)

// Epic 50 story 50.31, through newAPI's real wiring: the api role resolves the client IP from
// server.trusted_proxies before its rate limiters run. A CF-Connecting-IP from an untrusted
// peer can't buy a fresh bucket; one from a trusted peer (cloudflared on loopback) names the
// real client, so two clients behind the same connector don't share a bucket.
func TestAPIClientIPFromTrustedProxiesOnly(t *testing.T) {
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Server.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	})
	// The mail routes allow a burst of 5 per IP.
	forgot := func(peer, cf string) int {
		c := e.client()
		c.ip = peer
		c.header = map[string]string{"CF-Connecting-IP": cf}
		return c.do("POST", "/v1/auth/password/forgot", map[string]string{"email": "nobody@example.test"}).code
	}

	t.Run("untrusted peer: the header is ignored", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			if code := forgot("203.0.113.9", fmt.Sprintf("198.51.100.%d", 100+i)); code == 429 {
				t.Fatalf("request %d limited early", i+1)
			}
		}
		if code := forgot("203.0.113.9", "198.51.100.200"); code != 429 {
			t.Fatalf("a forged CF-Connecting-IP got a fresh bucket: %d", code)
		}
	})

	t.Run("trusted peer: the header names the client", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			if code := forgot("127.0.0.1", "192.0.2.1"); code == 429 {
				t.Fatalf("request %d limited early", i+1)
			}
		}
		if code := forgot("127.0.0.1", "192.0.2.1"); code != 429 {
			t.Fatalf("the same client behind the connector was not limited: %d", code)
		}
		if code := forgot("127.0.0.1", "192.0.2.2"); code == 429 {
			t.Fatal("a second client behind the connector shared the first one's bucket")
		}
	})
}

// main.go logs config.Warnings at start: the warning it would print for this config.
func TestServeWarnsAboutAnUnusedAOPCAFile(t *testing.T) {
	var c config.Config
	c.Server.AOPCAFile = "/etc/maktaba/aop-ca.pem"
	if w := config.Warnings(c); len(w) != 1 {
		t.Fatalf("warnings %q", w)
	}
}
