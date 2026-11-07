//go:build integration

package directjobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type fakeIssuer struct {
	mu     sync.Mutex
	refuse map[string]bool // CAs that refuse
	asked  []string
}

func (f *fakeIssuer) Issue(_ context.Context, _ []byte, homeID, domain, ca string) ([]byte, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, ca)
	if f.refuse[ca] {
		return nil, time.Time{}, errors.New(ca + ": too many certificates")
	}
	return []byte("CHAIN from " + ca + " for " + direct.Wildcard(homeID, domain)), time.Now().Add(90 * 24 * time.Hour), nil
}

type fakeChecker struct{ result string }

func (f fakeChecker) Check(context.Context, netip.Addr, int, string, string) string { return f.result }

// T12 in part: a CA that refuses a first certificate passes it to the next; the renewal stays
// with the CA that issued. And a due check lands.
func TestIssuingAndChecking(t *testing.T) {
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
	pick := func(use []direct.DomainUse) (string, string, bool) {
		return direct.Pick([]string{"direct-a.example"}, use, 45)
	}
	if _, _, err := st.PutCSR(ctx, id, []byte("csr"), pick, time.Now()); err != nil {
		t.Fatal(err)
	}
	iss := &fakeIssuer{refuse: map[string]bool{direct.CALetsEncrypt: true}}
	var overflowed []string
	j := &Jobs{Store: st, Issuer: iss, Checker: fakeChecker{direct.CheckOK}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Overflow: func(domain, ca string) { overflowed = append(overflowed, ca) }}
	if n := j.IssueOnce(ctx); n != 1 {
		t.Fatalf("issued %d", n)
	}
	c, _ := st.Cert(ctx, id)
	if c.CA != direct.CAZeroSSL || c.CertPEM == "" || len(overflowed) != 1 {
		t.Fatalf("overflow: %+v %v %v", c, iss.asked, overflowed)
	}
	// renewal: from ZeroSSL again, even though Let's Encrypt would now take it
	iss.refuse = map[string]bool{}
	_, _, _ = st.PutCSR(ctx, id, []byte("csr2"), pick, time.Now())
	iss.asked = nil
	j.IssueOnce(ctx)
	if len(iss.asked) != 1 || iss.asked[0] != direct.CAZeroSSL {
		t.Fatalf("the renewal asked %v", iss.asked)
	}
	// a renewal ZeroSSL refuses stays with ZeroSSL and keeps its error
	iss.refuse = map[string]bool{direct.CAZeroSSL: true}
	_, _, _ = st.PutCSR(ctx, id, []byte("csr3"), pick, time.Now())
	iss.asked = nil
	j.IssueOnce(ctx)
	if c, _ := st.Cert(ctx, id); len(iss.asked) != 1 || c.Error == "" || c.CSR == nil {
		t.Fatalf("renewal refused: %v %+v", iss.asked, c)
	}

	_ = st.SetEndpoints(ctx, id, netip.MustParseAddr("81.2.3.4"), 32443, "on")
	if n := j.CheckOnce(ctx); n != 1 {
		t.Fatalf("checked %d", n)
	}
	if r, _ := st.ReachableFor(ctx, []string{id}); r[id].Port != 32443 {
		t.Fatalf("reachable %+v", r)
	}
}
