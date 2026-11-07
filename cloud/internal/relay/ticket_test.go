package relay

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// T02: good, expired, bad MAC, other server.
func TestTickets(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tk := NewTickets([]byte(strings.Repeat("k", 32)))
	tk.now = func() time.Time { return now }
	ticket := tk.Mint("srv-a")
	if ticket == "" {
		t.Fatal("no ticket minted")
	}
	if err := tk.Verify(ticket, "srv-a"); err != nil {
		t.Fatalf("good ticket: %v", err)
	}
	if err := tk.Verify(ticket, "srv-b"); !errors.Is(err, ErrTicketWrongServer) {
		t.Errorf("other server: %v, want wrong_server", err)
	}
	// a flipped MAC byte
	b := []byte(ticket)
	if b[len(b)-2] == 'A' {
		b[len(b)-2] = 'B'
	} else {
		b[len(b)-2] = 'A'
	}
	if err := tk.Verify(string(b), "srv-a"); !errors.Is(err, ErrTicketBadMAC) {
		t.Errorf("bad mac: %v, want bad_mac", err)
	}
	// a ticket of another secret (a rotated token secret) is refused
	other := NewTickets([]byte(strings.Repeat("x", 32)))
	if err := other.Verify(ticket, "srv-a"); !errors.Is(err, ErrTicketBadMAC) {
		t.Errorf("rotated secret: %v, want bad_mac", err)
	}
	// a node with the same secret accepts it (failover, node A → node B)
	nodeB := NewTickets([]byte(strings.Repeat("k", 32)))
	nodeB.now = tk.now
	if err := nodeB.Verify(ticket, "srv-a"); err != nil {
		t.Errorf("node B: %v", err)
	}
	now = now.Add(TicketTTL)
	if err := tk.Verify(ticket, "srv-a"); !errors.Is(err, ErrTicketExpired) {
		t.Errorf("expired: %v, want expired", err)
	}
	for _, bad := range []string{"", "!!!", "AAAA"} {
		if err := tk.Verify(bad, "srv-a"); !errors.Is(err, ErrTicketMalformed) {
			t.Errorf("malformed %q: %v", bad, err)
		}
	}
	var off *Tickets
	if off.Mint("srv-a") != "" || off.Verify(ticket, "srv-a") == nil {
		t.Error("nil Tickets must mint nothing and refuse everything")
	}
	if NewTickets(nil) != nil {
		t.Error("an empty secret turns tickets off")
	}
}

// T09: a ticket check is an HMAC, far under 5 ms.
func BenchmarkTicketVerify(b *testing.B) {
	tk := NewTickets([]byte(strings.Repeat("k", 32)))
	ticket := tk.Mint("6f1c2a8e-1111-4222-8333-944445555666")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tk.Verify(ticket, "6f1c2a8e-1111-4222-8333-944445555666"); err != nil {
			b.Fatal(err)
		}
	}
}

// The handover counters render as Prometheus series with their labels.
func TestWriteMetrics(t *testing.T) {
	var b strings.Builder
	if err := WriteMetrics(&b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`maktaba_relay_tunnel_drains_total{reason="shutdown"}`,
		`maktaba_relay_tunnel_drain_forced_total `,
		`maktaba_relay_tunnel_handshakes_total{method="ticket"}`,
		`maktaba_relay_tunnel_handshakes_total{method="bearer"}`,
		`# TYPE maktaba_relay_tunnel_tickets_refused_total counter`,
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %s in\n%s", want, b.String())
		}
	}
}
