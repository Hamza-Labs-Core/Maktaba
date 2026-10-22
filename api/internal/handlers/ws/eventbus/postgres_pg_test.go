package eventbus

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// #157: a listener whose connection string was settled while the database did not
// answer (sslmode=require kept, on a server with no TLS) cannot connect; it is settled
// again and made anew, and the notifications come. Opt-in: MAKTABA_TEST_PG_URL on a
// server without TLS (sslmode=disable).
func TestTheListenerIsSettledAgain(t *testing.T) {
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" || !strings.Contains(base, "sslmode=disable") {
		t.Skip("MAKTABA_TEST_PG_URL unset or not a server without TLS (sslmode=disable)")
	}
	db, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := NewPostgresBackend(db, strings.Replace(base, "sslmode=disable", "sslmode=require", 1), nil)
	settled := 0
	p.Resettle = func(context.Context) string { settled++; return base }
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ch, err := p.Listen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case n, ok := <-ch:
			if !ok {
				t.Fatal("the stream ended")
			}
			if n.ID == 7 && n.Channel == "c" {
				if settled == 0 {
					t.Fatal("came without being settled again")
				}
				return
			}
		case <-tick.C:
			if _, err := db.Exec(`SELECT pg_notify('ws.events', '{"id":7,"channel":"c"}')`); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("no notification: the listener was not settled again")
		}
	}
}
