//go:build integration

package account

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Signing out everywhere ends the account's reset links, and no other account's.
func TestSigningOutEverywhereEndsTheResetLinks(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	users, tokens := stores.NewUsers(pool.DB), stores.NewMailTokens(pool.DB)
	d := &Deps{Users: users, Sessions: sessions.New(pool.DB), Tokens: tokens, Audit: audit.New(pool.DB), Logger: slog.New(slog.DiscardHandler)}
	link := func(email string) (uid, tok string) {
		t.Helper()
		u, err := users.Create(ctx, email, "hash", "", true)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tok, err = tokens.IssueTx(ctx, tx, u.ID, stores.PurposePasswordReset, stores.PasswordResetTTL); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return u.ID, tok
	}
	uid, mine := link("me@example.test")
	_, theirs := link("them@example.test")
	rec := httptest.NewRecorder()
	d.RevokeAllSessions(rec, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(middleware.WithUserID(ctx, uid)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, err := tokens.Redeem(ctx, mine, stores.PurposePasswordReset); !errors.Is(err, stores.ErrTokenInvalid) {
		t.Errorf("the link outlived it: %v", err)
	}
	if _, err := tokens.Redeem(ctx, theirs, stores.PurposePasswordReset); err != nil {
		t.Errorf("another account's link: %v", err)
	}
}
