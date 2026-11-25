//go:build integration

package stores

import (
	"context"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

// A link made in a transaction that does not commit is none, and leaves the one before it good;
// one that commits retires it. RegisterTx says whether the address was free, and fails on none.
func TestTokensAndRegistrationAreTheirCallersTransaction(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	tokens, users := NewMailTokens(pool.DB), NewUsers(pool.DB)
	register := func(commit bool) bool {
		t.Helper()
		tx, err := pool.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		free, err := users.RegisterTx(ctx, tx, " A@Example.test ", "hash", "", "ar")
		if err != nil {
			t.Fatal(err)
		}
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		return free
	}
	if !register(false) || !register(true) || register(true) {
		t.Fatal("free, free again after a rollback, then taken")
	}
	u, err := users.ByEmail(ctx, "a@example.test")
	if err != nil || u.Locale != "ar" || u.EmailVerified || u.DisplayName.Valid {
		t.Fatalf("%+v %v", u, err)
	}
	issue := func(commit bool) string {
		t.Helper()
		tx, err := pool.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		tok, err := tokens.IssueTx(ctx, tx, u.ID, PurposePasswordReset, PasswordResetTTL)
		if err != nil {
			t.Fatal(err)
		}
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		return tok
	}
	first, gone := issue(true), issue(false)
	if _, err := tokens.Redeem(ctx, gone, PurposePasswordReset); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a link that was never kept: %v", err)
	}
	second := issue(true)
	if _, err := tokens.Redeem(ctx, first, PurposePasswordReset); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("the link before: %v", err)
	}
	if id, err := tokens.Redeem(ctx, second, PurposePasswordReset); err != nil || id != u.ID {
		t.Fatalf("%s %v", id, err)
	}
}
