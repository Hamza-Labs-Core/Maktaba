//go:build integration

package stores

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
)

// Story 50.15: a home's domain is chosen once and kept; a renewal doesn't count against the
// week; a check reaches the home list only when it passed; challenges serve the responder.
func TestDirectState(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	d := NewDirect(pool.DB)
	owner, err := NewUsers(pool.DB).Create(ctx, "o-"+uuid.NewString()+"@example.com", "x", "O", false)
	if err != nil {
		t.Fatal(err)
	}
	home := func() string {
		id := uuid.NewString()
		if _, err := pool.DB.ExecContext(ctx, `INSERT INTO servers (id, owner_user_id, name, slug, server_secret_hash) VALUES ($1, $2, 'h', $3, 'x')`,
			id, owner.ID, "s"+id[:8]); err != nil {
			t.Fatal(err)
		}
		return id
	}
	domains := []string{"direct-a.example", "direct-b.example"}
	pick := func(use []direct.DomainUse) (string, string, bool) { return direct.Pick(domains, use, 1) }
	now := time.Now()

	a, b := home(), home()
	ca, over, err := d.PutCSR(ctx, a, []byte("csr-a"), pick, now)
	if err != nil || ca.Domain != "direct-a.example" || over {
		t.Fatalf("first home: %+v %v %v", ca, over, err)
	}
	if err := d.SetIssued(ctx, a, ca.CA, "CHAIN-A", now.Add(90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// the budget is 1 here: direct-a has used its week, so the next home gets direct-b
	cb, _, err := d.PutCSR(ctx, b, []byte("csr-b"), pick, now)
	if err != nil || cb.Domain != "direct-b.example" {
		t.Fatalf("second home: %+v %v", cb, err)
	}
	// a renewal keeps the domain and doesn't count as new
	again, _, err := d.PutCSR(ctx, a, []byte("csr-a2"), pick, now)
	if err != nil || again.Domain != "direct-a.example" {
		t.Fatalf("renewal: %+v %v", again, err)
	}
	// a home asks its name before its CSR: assigned once, the same on the next ask, not pending
	c := home()
	n1, _, err := d.Assign(ctx, c, pick, now)
	if err != nil || n1.Domain == "" || n1.HomeID != direct.HomeID(c) || n1.CSR != nil {
		t.Fatalf("a name: %+v %v", n1, err)
	}
	if n2, _, err := d.Assign(ctx, c, pick, now); err != nil || n2.Domain != n1.Domain {
		t.Fatalf("asked again: %+v %v", n2, err)
	}
	pend, _ := d.PendingCSRs(ctx, 10)
	if len(pend) != 2 {
		t.Fatalf("pending: %d", len(pend))
	}
	_ = d.SetIssued(ctx, a, ca.CA, "CHAIN-A2", now.Add(90*24*time.Hour))
	use, _ := d.DomainUse(ctx, now)
	for _, u := range use {
		if u.Domain == "direct-a.example" && u.IssuedWeek != 1 {
			t.Fatalf("a renewal counted as new: %+v", u)
		}
	}
	if chain, dom, _ := d.CertToDeliver(ctx, a); chain != "CHAIN-A2" || dom != "direct-a.example" {
		t.Fatalf("to deliver: %q %q", chain, dom)
	}
	if chain, _, _ := d.CertToDeliver(ctx, a); chain != "" {
		t.Fatal("delivered twice")
	}

	// the report, the check, the home list
	if err := d.SetEndpoints(ctx, a, netip.MustParseAddr("81.2.3.4"), 32443, "on"); err != nil {
		t.Fatal(err)
	}
	due, err := d.DueChecks(ctx, 15*time.Minute, 10)
	if err != nil || len(due) != 1 || due[0].IP != netip.MustParseAddr("81.2.3.4") || due[0].Port != 32443 || due[0].HomeID != direct.HomeID(a) {
		t.Fatalf("due: %+v %v", due, err)
	}
	if r, _ := d.ReachableFor(ctx, []string{a, b}); len(r) != 0 {
		t.Fatal("listed before a check passed")
	}
	_ = d.SetCheck(ctx, a, direct.CheckOK, 32443)
	if r, _ := d.ReachableFor(ctx, []string{a, b}); r[a].Port != 32443 || r[a].Domain != "direct-a.example" {
		t.Fatalf("reachable: %+v", r)
	}
	if res, port, _, _ := d.CheckToDeliver(ctx, a); res != direct.CheckOK || port != 32443 {
		t.Fatalf("check to deliver: %s %d", res, port)
	}
	if due, _ := d.DueChecks(ctx, 15*time.Minute, 10); len(due) != 0 {
		t.Fatal("checked just now: not due")
	}
	_ = d.RequestCheck(ctx, a)
	if due, _ := d.DueChecks(ctx, 15*time.Minute, 10); len(due) != 1 {
		t.Fatal("Check now: due at once")
	}
	// a new port, or a failed check: off the list
	_ = d.SetEndpoints(ctx, a, netip.MustParseAddr("81.2.3.4"), 40000, "on")
	if r, _ := d.ReachableFor(ctx, []string{a}); len(r) != 0 {
		t.Fatal("a port the check didn't see is not listed")
	}
	_ = d.SetEndpoints(ctx, b, netip.MustParseAddr("100.64.1.2"), 0, direct.CheckCGNAT)
	if res, _, _, _ := d.CheckToDeliver(ctx, b); res != direct.CheckCGNAT {
		t.Fatalf("cgnat: %q", res)
	}

	// challenges
	name := "_acme-challenge." + direct.HomeID(a) + ".direct-a.example"
	_ = d.PutChallenge(ctx, name, "v1", time.Minute)
	if v, _ := d.ChallengeTXT(ctx, name); len(v) != 1 || v[0] != "v1" {
		t.Fatalf("txt: %v", v)
	}
	_ = d.DeleteChallenge(ctx, name, "v1")
	if v, _ := d.ChallengeTXT(ctx, name); len(v) != 0 {
		t.Fatalf("deleted: %v", v)
	}
}

// Story 50.15 §3 (DNS rebinding): the private LAN addresses of a home's report are kept (at
// most MaxLAN, a public, link-local or port-less one dropped) and listed with its certificate's
// names while direct access is on, behind CGNAT or unmapped; off, or with no certificate, not.
func TestDirectLAN(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	d := NewDirect(pool.DB)
	owner, err := NewUsers(pool.DB).Create(ctx, "o-"+uuid.NewString()+"@example.com", "x", "O", false)
	if err != nil {
		t.Fatal(err)
	}
	home := func() string {
		id := uuid.NewString()
		if _, err := pool.DB.ExecContext(ctx, `INSERT INTO servers (id, owner_user_id, name, slug, server_secret_hash) VALUES ($1, $2, 'h', $3, 'x')`,
			id, owner.ID, "s"+id[:8]); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pick := func(use []direct.DomainUse) (string, string, bool) {
		return direct.Pick([]string{"direct-a.example"}, use, 45)
	}
	a, nocert := home(), home()
	if _, _, err := d.PutCSR(ctx, a, []byte("csr"), pick, time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = d.SetIssued(ctx, a, "letsencrypt", "CHAIN", time.Now().Add(90*24*time.Hour))
	ap := netip.MustParseAddrPort
	report := []netip.AddrPort{ap("81.2.3.4:8444"), ap("192.168.1.20:8444"), ap("[fe80::1%eth0]:8444"),
		ap("10.0.0.5:0"), ap("[fd00::7]:8444"), ap("10.0.0.6:8444"), ap("10.0.0.7:8444"), ap("10.0.0.8:8444")}
	for _, id := range []string{a, nocert} {
		if err := d.SetEndpoints(ctx, id, netip.MustParseAddr("81.2.3.4"), 32443, "on"); err != nil {
			t.Fatal(err)
		}
		if err := d.SetLAN(ctx, id, report); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.LANFor(ctx, []string{a, nocert})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[nocert]; ok {
		t.Error("a home with no certificate listed")
	}
	h := got[a]
	want := []netip.AddrPort{ap("192.168.1.20:8444"), ap("[fd00::7]:8444"), ap("10.0.0.6:8444"), ap("10.0.0.7:8444")}
	if h.HomeID != direct.HomeID(a) || h.Domain != "direct-a.example" || len(h.LAN) != len(want) {
		t.Fatalf("%+v", h)
	}
	for i := range want {
		if h.LAN[i] != want[i] {
			t.Errorf("lan %d: %v, want %v", i, h.LAN[i], want[i])
		}
	}
	// behind CGNAT the LAN still works; turned off it is not listed; an empty report clears
	_ = d.SetEndpoints(ctx, a, netip.MustParseAddr("100.64.1.2"), 0, direct.CheckCGNAT)
	if got, _ := d.LANFor(ctx, []string{a}); len(got[a].LAN) != 4 {
		t.Errorf("cgnat: %+v", got)
	}
	_ = d.SetEndpoints(ctx, a, netip.Addr{}, 0, "off")
	if got, _ := d.LANFor(ctx, []string{a}); len(got) != 0 {
		t.Errorf("off: %+v", got)
	}
	_ = d.SetEndpoints(ctx, a, netip.MustParseAddr("81.2.3.4"), 32443, "on")
	_ = d.SetLAN(ctx, a, nil)
	if got, _ := d.LANFor(ctx, []string{a}); len(got) != 0 {
		t.Errorf("cleared: %+v", got)
	}
}
