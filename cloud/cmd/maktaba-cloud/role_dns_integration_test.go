//go:build integration

package main

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
)

// Story 50.15: the dns role starts for real (Validate refused `--role dns`
// as unknown, so the name server never started), answers a direct name
// over UDP and TCP from its own process, stays up, and stops on SIGTERM.
func TestDNSRoleStaysUpAndAnswers(t *testing.T) {
	listen := freeLoopbackAddr(t)
	p := startRole(t, "dns", `
[direct]
domains = ["direct-a.example"]
ns1 = "203.0.113.11"
ns2 = "203.0.113.12"
dns_listen = "`+listen+`"
`)
	if !strings.Contains(p.health, `"role":"dns"`) {
		t.Fatalf("healthz body %q, want the dns role's", p.health)
	}
	name := "10-0-0-7." + direct.HomeID("s1") + ".direct-a.example"
	for _, network := range []string{"udp", "tcp"} {
		res := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, listen)
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ips, err := res.LookupNetIP(ctx, "ip4", name)
		cancel()
		if err != nil || len(ips) != 1 || ips[0] != netip.MustParseAddr("10.0.0.7") {
			t.Fatalf("over %s: %v %v; output:\n%s", network, ips, err, p.out.String())
		}
	}
	p.staysUpThenStops(t)
}
