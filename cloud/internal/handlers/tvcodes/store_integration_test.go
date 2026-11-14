//go:build integration

package tvcodes

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// testDB is a scratch database of its own on MAKTABA_CLOUD_DB_URL, migrated to the head.
func testDB(t *testing.T) (*db.Pool, *db.Migrator, context.Context) {
	t.Helper()
	raw := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if raw == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	admin, err := db.Open(raw, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_tv_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(u.String(), 8, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.DB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	mig := db.NewMigrator(pool, migrations.FS, ".")
	if err := mig.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool, mig, ctx
}

func server(t *testing.T, ctx context.Context, d *sql.DB) string {
	t.Helper()
	var uid, sid string
	if err := d.QueryRowContext(ctx, `INSERT INTO users (email, password_hash, display_name) VALUES ('tv@example.org', 'x', 'Owner') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'Home', 'tvhome', 'x') RETURNING id`, uid).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

// T02: the migration makes the table and its expiry index, and goes down and up again.
func TestMigration00580001(t *testing.T) {
	pool, mig, ctx := testDB(t)
	exists := func() (table, index bool) {
		_ = pool.DB.QueryRowContext(ctx, `SELECT to_regclass('public.tv_codes') IS NOT NULL, to_regclass('public.idx_tv_codes_expires') IS NOT NULL`).Scan(&table, &index)
		return
	}
	if tb, ix := exists(); !tb || !ix {
		t.Fatalf("after up: table %v, index %v", tb, ix)
	}
	var def string
	if err := pool.DB.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_tv_codes_expires'`).Scan(&def); err != nil || !contains(def, "(expires_at)") {
		t.Fatalf("index: %q %v", def, err)
	}
	if err := mig.Down(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if tb, _ := exists(); tb {
		t.Fatal("the table is left after down")
	}
	if err := mig.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if tb, _ := exists(); !tb {
		t.Fatal("no table after up again")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestSQLStore(t *testing.T) {
	pool, _, ctx := testDB(t)
	s := &SQLStore{DB: pool.DB}
	sid := server(t, ctx, pool.DB)
	now := time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)
	h := hashSecret("secret")
	row := Row{Code: "ABCD2345", Platform: "tizen", AppVersion: "1.0", Country: "JO", ExpiresAt: now.Add(CodeTTL)}
	if err := s.Create(ctx, row, h, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, row, h, now); err != ErrExists {
		t.Fatalf("duplicate: %v", err)
	}
	if got, err := s.Open(ctx, row.Code, now); err != nil || got.Platform != "tizen" || got.Country != "JO" {
		t.Fatalf("open: %+v %v", got, err)
	}
	if res, _, err := s.Poll(ctx, row.Code, h, now, Interval); err != nil || res != PollPending {
		t.Fatalf("poll: %v %v", res, err)
	}
	if res, _, _ := s.Poll(ctx, row.Code, h, now.Add(time.Second), Interval); res != PollSlowDown {
		t.Fatalf("quick poll: %v", res)
	}
	if res, _, _ := s.Poll(ctx, row.Code, hashSecret("wrong"), now.Add(10*time.Second), Interval); res != PollNotFound {
		t.Fatalf("wrong secret: %v", res)
	}

	// two homes claim at once: one wins
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Claim(ctx, row.Code, sid, []byte("sealed"), now.Add(20*time.Second))
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) || (errs[0] != ErrClaimed && errs[1] != ErrClaimed) {
		t.Fatalf("claims: %v, %v", errs[0], errs[1])
	}
	if _, err := s.Open(ctx, row.Code, now.Add(21*time.Second)); err != ErrNotFound {
		t.Fatalf("open after claim: %v", err)
	}

	// two polls at once: the payload goes to one
	results := make([]PollResult, 2)
	payloads := make([][]byte, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], payloads[i], _ = s.Poll(ctx, row.Code, h, now.Add(30*time.Second), Interval)
		}()
	}
	wg.Wait()
	delivered := 0
	for i, r := range results {
		if r == PollDelivered {
			delivered++
			if string(payloads[i]) != "sealed" {
				t.Fatalf("payload %q", payloads[i])
			}
		} else if r != PollGone || payloads[i] != nil {
			t.Fatalf("the other poll: %v %q", r, payloads[i])
		}
	}
	if delivered != 1 {
		t.Fatalf("delivered %d times", delivered)
	}
	var left []byte
	var deliveredAt sql.NullTime
	if err := pool.DB.QueryRowContext(ctx, `SELECT payload_sealed, delivered_at FROM tv_codes WHERE code = $1`, row.Code).Scan(&left, &deliveredAt); err != nil || left != nil || !deliveredAt.Valid {
		t.Fatalf("after delivery: payload %q, delivered %v, %v", left, deliveredAt, err)
	}

	// an unclaimed code past its expiry: gone, and not claimable
	other := Row{Code: "WXYZ6789", ExpiresAt: now.Add(CodeTTL)}
	if err := s.Create(ctx, other, h, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, other.Code, sid, []byte("x"), now.Add(CodeTTL)); err != ErrNotFound {
		t.Fatalf("claim after expiry: %v", err)
	}
	if res, _, _ := s.Poll(ctx, other.Code, h, now.Add(CodeTTL), Interval); res != PollGone {
		t.Fatalf("poll after expiry: %v", res)
	}

	// T02's sweep: nothing before expiry + 1 h, both rows after
	if n, err := s.Sweep(ctx, now.Add(CodeTTL+SweepAfter-time.Second).Add(-SweepAfter)); err != nil || n != 0 {
		t.Fatalf("early sweep: %d %v", n, err)
	}
	if n, err := s.Sweep(ctx, now.Add(CodeTTL+SweepAfter+time.Second).Add(-SweepAfter)); err != nil || n != 2 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	var count int
	_ = pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM tv_codes`).Scan(&count)
	if count != 0 {
		t.Fatalf("%d rows left", count)
	}
}
