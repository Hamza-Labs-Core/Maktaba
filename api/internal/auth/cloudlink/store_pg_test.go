package cloudlink_test

// Story 58.03: a TV approved on maktaba.tv is recorded as a cloud sign-in of its link only while
// the link still names the cloud account that approved it, in one transaction that locks the link
// row, so an unlink or a relink to another account cannot slip between the check and the record.

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

const (
	tvUser  = "00000000-0000-4000-8000-0000005803e1"
	tvOther = "00000000-0000-4000-8000-0000005803e2"
)

func tvStore(t *testing.T) (*cloudlink.Store, *sql.DB) {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash) VALUES ($1, 'tv-user', 'x'), ($2, 'tv-other', 'x')`,
		tvUser, tvOther); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO home_user_links (user_id, cloud_user_id, scope) VALUES ($1, 'cloud-tv', 'play')`, tvUser); err != nil {
		t.Fatal(err)
	}
	return &cloudlink.Store{DB: db}, db
}

// family puts a live refresh token of a new family for userID, as the TV's mint does.
func family(t *testing.T, db *sql.DB, userID, fam string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO refresh_tokens (id, family_id, user_id, hash, expires_at)
		VALUES (gen_random_uuid(), $1, $2, $3, now() + interval '1 day')`, fam, userID, "h-"+fam); err != nil {
		t.Fatal(err)
	}
}

func live(t *testing.T, db *sql.DB, fam string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL`, fam).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordLinkedSignInOnlyForTheApprovingAccount(t *testing.T) {
	s, db := tvStore(t)
	ctx := context.Background()
	for name, tc := range map[string]struct{ user, cloud, fam string }{
		"no link":              {tvOther, "cloud-tv", "00000000-0000-4000-8000-00000058e001"},
		"another account":      {tvUser, "cloud-was", "00000000-0000-4000-8000-00000058e002"},
		"no approving account": {tvUser, "", "00000000-0000-4000-8000-00000058e003"},
	} {
		family(t, db, tc.user, tc.fam)
		if err := s.RecordLinkedSignIn(ctx, tc.fam, tc.user, tc.cloud, principal.CloudScopePlay); !errors.Is(err, cloudlink.ErrLinkChanged) {
			t.Fatalf("%s: %v", name, err)
		}
		if scope, err := s.ScopeOf(ctx, cloudlink.KindRefresh, tc.fam); err != nil || scope != "" {
			t.Fatalf("%s: recorded as %q, %v", name, scope, err)
		}
		if n := live(t, db, tc.fam); n != 0 {
			t.Fatalf("%s: %d live tokens of the refused family", name, n)
		}
	}
	const fam = "00000000-0000-4000-8000-00000058e004"
	family(t, db, tvUser, fam)
	if err := s.RecordLinkedSignIn(ctx, fam, tvUser, "cloud-tv", principal.CloudScopePlay); err != nil {
		t.Fatal(err)
	}
	if scope, err := s.ScopeOf(ctx, cloudlink.KindRefresh, fam); err != nil || scope != principal.CloudScopePlay {
		t.Fatalf("scope = %q, %v", scope, err)
	}
	if n := live(t, db, fam); n != 1 {
		t.Fatalf("%d live tokens of the recorded family", n)
	}
}

// A relink to another account while a TV's record holds the link row waits for it, then ends
// the TV's sign-in with the old link's others: the TV never outlives the account that approved it.
func TestCompleteLinkWaitsForARecordInFlight(t *testing.T) {
	s, db := tvStore(t)
	ctx := context.Background()
	const fam = "00000000-0000-4000-8000-00000058e005"
	family(t, db, tvUser, fam)
	nonce, _, err := s.StartLink(ctx, tvUser)
	if err != nil {
		t.Fatal(err)
	}

	// the record's transaction, as RecordLinkedSignIn runs it, held open
	rec, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec.Rollback() }()
	var linked string
	if err := rec.QueryRow(`SELECT cloud_user_id FROM home_user_links WHERE user_id = $1 FOR SHARE`, tvUser).Scan(&linked); err != nil || linked != "cloud-tv" {
		t.Fatalf("linked = %q, %v", linked, err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.CompleteLink(ctx, tvUser, cloudlink.Assertion{Sub: "cloud-new", Non: nonce, Jti: "relink-1",
			Exp: time.Now().Add(time.Minute).Unix()})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("relinked past a record in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := rec.Exec(`INSERT INTO cloud_sign_ins (id_sha256, kind, user_id, scope, created_at)
		VALUES (encode(sha256($1::bytea), 'hex'), 'refresh', $2, 'play', now())`, fam, tvUser); err != nil {
		t.Fatal(err)
	}
	if err := rec.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("relink: %v", err)
	}
	if n := live(t, db, fam); n != 0 {
		t.Fatalf("%d live tokens of the old link's TV after the relink", n)
	}
}
