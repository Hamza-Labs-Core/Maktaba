//go:build integration

package relay

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Story 50.15: a home's META_ENDPOINTS through DBDirect. The public address is the tunnel's
// source; a router reporting a shared external address is cgnat; the listener's private LAN
// addresses are kept for the home list's LAN entries (§3), a bad one dropped.
func TestDBDirectEndpoints(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	st := stores.NewDirect(pool.DB)
	owner, err := stores.NewUsers(pool.DB).Create(ctx, "o-"+uuid.NewString()+"@example.com", "x", "O", false)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO servers (id, owner_user_id, name, slug, server_secret_hash) VALUES ($1, $2, 'h', $3, 'x')`,
		id, owner.ID, "s"+id[:8]); err != nil {
		t.Fatal(err)
	}
	d := &DBDirect{Store: st, Domains: func() []string { return []string{"direct-a.example"} }}
	if _, err := d.Name(ctx, id); err != nil {
		t.Fatal(err)
	}
	_ = st.SetIssued(ctx, id, "letsencrypt", "CHAIN", time.Now().Add(90*24*time.Hour))

	err = d.Endpoints(ctx, id, netip.MustParseAddr("81.2.3.4"), EndpointsPayload{Port: 32443, State: "on",
		LAN: []string{"192.168.1.20:8444", "not an address", "81.2.3.4:8444"}, External: "81.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	var state string
	var ip, port any
	_ = pool.DB.QueryRowContext(ctx, `SELECT direct_state, host(direct_ip), direct_port FROM servers WHERE id = $1`, id).Scan(&state, &ip, &port)
	if state != "on" || ip != "81.2.3.4" {
		t.Fatalf("state %q ip %v port %v", state, ip, port)
	}
	got, err := st.LANFor(ctx, []string{id})
	if err != nil || len(got[id].LAN) != 1 || got[id].LAN[0] != netip.MustParseAddrPort("192.168.1.20:8444") {
		t.Fatalf("lan: %+v %v", got, err)
	}

	// the router's external address is carrier-grade NAT: cgnat, and the LAN still listed
	if err := d.Endpoints(ctx, id, netip.MustParseAddr("81.2.3.4"), EndpointsPayload{Port: 32443, State: "on",
		LAN: []string{"192.168.1.20:8444"}, External: "100.64.0.9"}); err != nil {
		t.Fatal(err)
	}
	_ = pool.DB.QueryRowContext(ctx, `SELECT direct_state FROM servers WHERE id = $1`, id).Scan(&state)
	if state != direct.CheckCGNAT {
		t.Fatalf("state %q, want cgnat", state)
	}
	if got, _ := st.LANFor(ctx, []string{id}); len(got[id].LAN) != 1 {
		t.Fatalf("cgnat lan: %+v", got)
	}
}
