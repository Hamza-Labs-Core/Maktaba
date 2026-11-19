package subscriptions

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/testpg"
)

// The seat guard's statements on Postgres: the users table as slot 0029 makes it (UUID
// ids, a boolean is_admin, the sentinel admin row), in a schema of the test's own.
func TestTheSeatGuardOnPostgres(t *testing.T) {
	base := testpg.Get()
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset")
	}
	db, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1) // one connection: the search_path below is its own
	schema := "seats_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	for _, q := range []string{
		`CREATE SCHEMA ` + schema,
		`SET search_path = ` + schema,
		`CREATE TABLE users (id UUID PRIMARY KEY, username TEXT NOT NULL, pw_hash TEXT NOT NULL,
			is_admin BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`INSERT INTO users (id, username, pw_hash, is_admin) VALUES
			('00000000-0000-0000-0000-000000000001', 'admin', '<unsalted-disabled>', true)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Skipf("scratch Postgres: %s: %v", q, err)
		}
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var newest string
	for i, admin := range []bool{true, false, false, false} {
		id := uuid.NewString()
		if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin, created_at) VALUES ($1, $2, 'h', $3, $4)`,
			id, fmt.Sprintf("u%d", i), admin, at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		newest = id
	}
	g := &SeatGuard{DB: db, Store: NewStore()} // Free: 1 seat, 4 real accounts: 3 over, 3 non-admins
	got, err := g.ReadOnlyUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != newest || got[0].Username != "u3" {
		t.Fatalf("read-only = %+v", got)
	}
	g.Store = homeStore(t, 0) // 4 seats
	if got, _ := g.ReadOnlyUsers(context.Background()); len(got) != 0 {
		t.Fatalf("with the seats: %+v", got)
	}
}
