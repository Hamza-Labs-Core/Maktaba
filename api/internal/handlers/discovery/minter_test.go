package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/refresh"
)

// captureSigner records the claims it was asked to sign so a test can
// assert the `lib[]` snapshot without standing up an RSA key set — the
// interface-seam convention the minter is built around.
type captureSigner struct {
	got jwt.Claims
}

func (c *captureSigner) Sign(claims jwt.Claims) (string, error) {
	c.got = claims
	return "signed.access.token", nil
}

// fakeUserResolver / fakeLibResolver are the unit-test stand-ins for the
// users + ACL seams (mirrors the subscriptions/authz mock convention).
type fakeUserResolver struct {
	admin bool
	err   error
}

func (f fakeUserResolver) IsAdmin(_ context.Context, _ string) (bool, error) {
	return f.admin, f.err
}

type fakeLibResolver struct {
	libs map[string][]string
	err  error
}

func (f fakeLibResolver) LibrariesFor(_ context.Context, userID string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.libs[userID], nil
}

type fakeRefreshIssuer struct{}

func (fakeRefreshIssuer) Issue(_ context.Context, in refresh.IssueInput) (*refresh.Token, error) {
	return &refresh.Token{
		Plaintext: "mkt_rt_v1.row." + in.UserID,
		ExpiresAt: time.Now().Add(in.TTL),
	}, nil
}

func newMinter(u userResolver, acl libraryResolver, sgn jwtSigner) *tokenMinter {
	return &tokenMinter{
		users:   u,
		libs:    acl,
		refresh: fakeRefreshIssuer{},
		signer:  sgn,
		now:     func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
	}
}

// TestMintNonAdminCarriesLibraryACL is the Defect-1 regression: a paired
// non-admin's access JWT must carry the user's library ACL in `lib[]`
// (the exact source of truth native login uses) and MUST exclude a
// library the user is not entitled to. Without this the paired user is
// default-denied on every library read/search and the flow dead-ends.
func TestMintNonAdminCarriesLibraryACL(t *testing.T) {
	sgn := &captureSigner{}
	acl := fakeLibResolver{libs: map[string][]string{
		"user-1": {"lib-a", "lib-b"},
		// "lib-secret" exists for other users but user-1 is NOT entitled.
	}}
	m := newMinter(fakeUserResolver{admin: false}, acl, sgn)

	if _, err := m.Mint(context.Background(), "user-1", "phone", "Pixel 8"); err != nil {
		t.Fatalf("Mint: %v", err)
	}

	got := sgn.got.Lib
	if len(got) != 2 || got[0] != "lib-a" || got[1] != "lib-b" {
		t.Fatalf("lib[] = %v, want [lib-a lib-b]", got)
	}
	for _, l := range got {
		if l == "lib-secret" {
			t.Fatalf("non-entitled library leaked into lib[]: %v", got)
		}
	}
	if sgn.got.IsAdmin {
		t.Fatal("non-admin minted with is_admin=true")
	}
}

// TestMintAdminGetsEmptyLibAllLibraries: admins keep native login's
// admin-implies-all semantics — the ACL lookup is skipped and `lib[]`
// is empty (the verifier sets AccessAllLibraries from is_admin).
func TestMintAdminGetsEmptyLibAllLibraries(t *testing.T) {
	sgn := &captureSigner{}
	acl := fakeLibResolver{libs: map[string][]string{"admin-1": {"should-not-be-read"}}}
	m := newMinter(fakeUserResolver{admin: true}, acl, sgn)

	if _, err := m.Mint(context.Background(), "admin-1", "tv", "Living Room"); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(sgn.got.Lib) != 0 {
		t.Fatalf("admin lib[] = %v, want empty (admin-implies-all)", sgn.got.Lib)
	}
	if !sgn.got.IsAdmin {
		t.Fatal("admin minted with is_admin=false")
	}
}

// TestMintNonAdminNoEntitlementsEmptyLib: a non-admin with no ACL rows
// gets an empty (non-nil) slice — default-deny, never a panic.
func TestMintNonAdminNoEntitlementsEmptyLib(t *testing.T) {
	sgn := &captureSigner{}
	m := newMinter(fakeUserResolver{admin: false}, fakeLibResolver{libs: map[string][]string{}}, sgn)

	if _, err := m.Mint(context.Background(), "nobody", "phone", ""); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if sgn.got.Lib == nil {
		t.Fatal("lib[] is nil; want empty non-nil slice")
	}
	if len(sgn.got.Lib) != 0 {
		t.Fatalf("lib[] = %v, want empty", sgn.got.Lib)
	}
}

// TestMintPropagatesACLError: an ACL lookup failure must fail the mint
// (fail-closed), not silently issue a scope-less token.
func TestMintPropagatesACLError(t *testing.T) {
	sgn := &captureSigner{}
	m := newMinter(fakeUserResolver{admin: false}, fakeLibResolver{err: errors.New("acl down")}, sgn)

	if _, err := m.Mint(context.Background(), "user-1", "phone", ""); err == nil {
		t.Fatal("expected error when ACL lookup fails, got nil")
	}
}
