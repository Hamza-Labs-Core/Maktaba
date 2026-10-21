package nodemode

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func settingsDB(t *testing.T, value string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if value != "" {
		if _, err := db.Exec(`INSERT INTO app_settings VALUES ('rules.cluster', ?)`, value); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestLoadRules(t *testing.T) {
	ctx := context.Background()
	if got, err := LoadRules(ctx, settingsDB(t, "")); err != nil || got != DefaultRules() {
		t.Fatalf("no row: %+v %v, want the defaults and no error", got, err)
	}
	got, err := LoadRules(ctx, settingsDB(t, `{"offline_session_grace_minutes": 0, "delegate_unreachable_grace_seconds": 2,
		"offline_copy_max_lag_minutes": 0, "offline_session_hours": 24, "replica_wal_keep_gb": 4096}`))
	d := DefaultRules()
	if err != nil || got.OfflineSessionGraceMinutes != 0 || got.GraceSeconds != d.GraceSeconds ||
		got.OfflineCopyMaxLagMinutes != d.OfflineCopyMaxLagMinutes || got.OfflineSessionHours != 24 ||
		got.ReplicaWALKeepGB != d.ReplicaWALKeepGB {
		t.Fatalf("0 minutes of grace and 24 h are values; 2 s of grace, a 0 lag and 4 TB are out of range: %+v %v", got, err)
	}
	db := settingsDB(t, "")
	_ = db.Close()
	if got, err := LoadRules(ctx, db); err == nil || got != DefaultRules() {
		t.Fatalf("a failed read: %+v %v, want the defaults with the error", got, err)
	}
}

func TestCachedRulesKeepTheLastGoodSection(t *testing.T) {
	db := settingsDB(t, `{"offline_copy_max_lag_minutes": 30}`)
	rules := CachedRules(db, 0) // read every time
	if got := rules().OfflineCopyMaxLagMinutes; got != 30 {
		t.Fatalf("read: %d", got)
	}
	_ = db.Close() // the home's database goes away
	if got := rules().OfflineCopyMaxLagMinutes; got != 30 {
		t.Fatalf("after a failed read: %d, want the last good 30 (not the default)", got)
	}
}

func TestUnreachableTellsADeadDatabaseFromARefusal(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connectex: No connection could be made")}
	for _, err := range []error{
		dial,
		fmt.Errorf("%w: cannot read the role's privileges: %w", ErrDDLRole, dial),
		context.DeadlineExceeded,
		driver.ErrBadConn,
		io.ErrUnexpectedEOF,
	} {
		if !Unreachable(err) {
			t.Fatalf("not unreachable: %v", err)
		}
	}
	for _, err := range []error{
		nil,
		errors.New(`pq: password authentication failed for user "node_6f1c"`),
		fmt.Errorf("%w: role %q has CREATEDB", ErrDDLRole, "node"),
	} {
		if Unreachable(err) {
			t.Fatalf("unreachable: %v", err)
		}
	}
}

func TestCheckRoleKeepsTheCause(t *testing.T) {
	db, err := sql.Open("postgres", "postgres://node@127.0.0.1:1/maktaba?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = CheckRole(context.Background(), db)
	if !errors.Is(err, ErrDDLRole) || !Unreachable(err) {
		t.Fatalf("a database that does not answer: %v (unreachable %v)", err, Unreachable(err))
	}
}

// #145: a caller is never held up by a database that does not answer: past the first
// read, the section is read again in the background while the last one is answered.
func TestCachedRulesDoNotWaitOnTheDatabase(t *testing.T) {
	db := settingsDB(t, `{"offline_copy_max_lag_minutes": 30}`)
	rules := CachedRules(db, 0)
	if got := rules().OfflineCopyMaxLagMinutes; got != 30 {
		t.Fatalf("read: %d", got)
	}
	// the database's one connection is held: a read waits on it
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	for i := 0; i < 3; i++ {
		if got := rules().OfflineCopyMaxLagMinutes; got != 30 {
			t.Fatalf("while the database is busy: %d", got)
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("the callers waited %s on the database", d)
	}
}
