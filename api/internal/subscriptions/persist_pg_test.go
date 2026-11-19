package subscriptions

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/testpg"
)

// The SQL license store's ended rows (story 16.2): a same-id plan license kept as a row
// of its own, a revoked one's row made no grace, both read back by LoadEnded.
func TestTheSQLStoreKeepsEndedLicenses(t *testing.T) {
	ctx := context.Background()
	base := testpg.Get()
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset")
	}
	db, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1) // the search_path below is this connection's
	schema := "lic_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	for _, q := range []string{
		`CREATE SCHEMA ` + schema,
		`SET search_path = ` + schema,
		// slots 0056 + 0063 and 0155, as they leave the tables
		`CREATE TABLE licenses (license_id TEXT PRIMARY KEY,
			tier TEXT NOT NULL CHECK (tier IN ('free', 'home', 'pro')), seats INTEGER NOT NULL CHECK (seats >= 0),
			issued_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ,
			raw_jwt TEXT NOT NULL, features JSONB NOT NULL DEFAULT '[]')`,
		`CREATE UNIQUE INDEX licenses_only_one_active ON licenses ((revoked_at IS NULL)) WHERE revoked_at IS NULL`,
		`CREATE TABLE license_state (id INTEGER PRIMARY KEY CHECK (id = 1), revocations TEXT NOT NULL DEFAULT '',
			revocations_at TIMESTAMPTZ, clock_high_water TIMESTAMPTZ, last_refresh_at TIMESTAMPTZ,
			last_refresh_error TEXT NOT NULL DEFAULT '', lock_reason TEXT NOT NULL DEFAULT '')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Skipf("scratch Postgres: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	pub, priv := newKey(t)
	v := &Verifier{PublicKey: pub}
	p := NewSQLLicensePersistence(db)
	s, err := NewPersistentStore(ctx, p, v)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "cloud-srv-1", Tier: TierPro, IssuedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: "srv-1"})
	applyLicense(t, s, v, priv, LicenseInner{LicenseID: "cloud-srv-1", Tier: TierHome, Seats: 4, IssuedAt: now,
		ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: "srv-1"})
	ended, err := p.(EndedPersistence).LoadEnded(ctx, now.Add(-Grace))
	if err != nil || len(ended) != 1 || ended[0].Tier != TierPro {
		t.Fatalf("ended rows: %+v %v", ended, err)
	}
	s2, err := NewPersistentStore(ctx, p, v)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Allows(FeatureFederation) || s2.State().License.Tier != TierHome {
		t.Fatalf("after a restart: %+v", s2.State())
	}
	// removed, then re-issued on the same id at the lower plan: the removed plan's end
	// is a row of its own, so the overwrite of the plain row keeps it
	if err := s2.Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	applyLicense(t, s2, v, priv, LicenseInner{LicenseID: "cloud-srv-1", Tier: TierHome, Seats: 2, IssuedAt: now.Add(time.Minute),
		ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: "srv-1"})
	s3, err := NewPersistentStore(ctx, p, v)
	if err != nil {
		t.Fatal(err)
	}
	if !s3.Allows(FeatureFederation) || s3.SeatLimit() != SeatsUnlimited {
		t.Fatalf("a removal then a same-id lower plan, after a restart: %+v", s3.State())
	}
	if err := p.(EndedPersistence).DropGrace(ctx, ended[0].LicenseID); err != nil {
		t.Fatal(err)
	}
	left, _ := p.(EndedPersistence).LoadEnded(ctx, now.Add(-Grace))
	for _, e := range left {
		if e.LicenseID == ended[0].LicenseID {
			t.Fatalf("a dropped grace is still read: %+v", e)
		}
	}
}
