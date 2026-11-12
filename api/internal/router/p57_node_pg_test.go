package router_test

// Epic 57 on a second machine (a full node, specs/epics/37-node-replica): the node
// serves the library from its own copy of the home's database, and the parental gate
// reads an account's limit from the same place. With the home's database down the
// copy answers both, so an account nothing restricts still browses and a restricted
// one is still filtered; with neither database readable nobody is shown content.
// Against a real migrated Postgres (MAKTABA_TEST_PG_URL): the "copy" is the scratch
// database, the "home" a server that refuses every connection.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodedb"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
)

// gatedConn is a database server that can be switched off: while off it refuses
// every new connection, as a server that is down does.
type gatedConn struct {
	inner driver.Connector
	off   atomic.Bool
}

var errServerDown = errors.New("connection refused (the test switched this server off)")

func (g *gatedConn) Connect(ctx context.Context) (driver.Conn, error) {
	if g.off.Load() || g.inner == nil {
		return nil, errServerDown
	}
	return g.inner.Connect(ctx)
}

func (g *gatedConn) Driver() driver.Driver {
	if g.inner != nil {
		return g.inner.Driver()
	}
	return downDriver{}
}

type downDriver struct{}

func (downDriver) Open(string) (driver.Conn, error) { return nil, errServerDown }

// nodeFilter is the filter's home (setupFilter's accounts and videos) served as a
// node serves it: through a pair of the home's database, which is down, and this
// machine's copy. It returns the copy's switch too.
func nodeFilter(t *testing.T) (*pfEnv, *gatedConn) {
	t.Helper()
	copySrv := &gatedConn{}
	e := setupFilterOn(t, nil, func(dsn string) *sql.DB {
		conn, err := pgdsn.Connector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		copySrv.inner = conn
		pair := &nodedb.Pair{
			Home: &nodedb.Source{Name: "home", Conn: &gatedConn{}}, // never answers
			Copy: &nodedb.Source{Name: "copy", Conn: copySrv},
		}
		db := pair.Open(time.Minute)
		// one connection at a time, none kept: a switched-off server is met by the
		// next statement, not hidden behind a connection opened before
		db.SetMaxIdleConns(0)
		t.Cleanup(func() { _ = db.Close() })
		return db
	})
	return e, copySrv
}

func TestP57NodeFiltersFromItsCopyWithTheHomeDown(t *testing.T) {
	e, copySrv := nodeFilter(t)
	lists := []string{"/api/videos?library_id=" + pfLibA, "/api/libraries", "/api/collections?library_id=" + pfLibA}

	// an account nothing restricts browses what the copy holds
	for _, path := range lists {
		code, body := e.get("viewer", path)
		if code != http.StatusOK {
			t.Fatalf("the viewer, GET %s with the home down: %d %s", path, code, body)
		}
	}
	if _, body := e.get("viewer", lists[0]); !strings.Contains(body, pfTeenName) || !strings.Contains(body, "Kid-Title") {
		t.Fatalf("the viewer's list from the copy lacks a video: %s", body)
	}
	if code, body := e.get("viewer", "/api/videos/"+pfTeen); code != http.StatusOK {
		t.Fatalf("the viewer, the 16+ video: %d %s", code, body)
	}

	// a restricted child (9+) is filtered by the limit the copy holds
	for _, path := range lists {
		code, body := e.get("child", path)
		if code != http.StatusOK {
			t.Fatalf("the child, GET %s with the home down: %d %s", path, code, body)
		}
		if strings.Contains(body, pfTeenName) || strings.Contains(body, pfTeen) || strings.Contains(body, "Unrated-Title") {
			t.Fatalf("the child, GET %s: a video above its limit is shown: %s", path, body)
		}
	}
	if _, body := e.get("child", lists[0]); !strings.Contains(body, "Kid-Title") {
		t.Fatalf("the child's list lacks the video it may watch: %s", body)
	}
	if code, body := e.get("child", "/api/videos/"+pfKid); code != http.StatusOK {
		t.Fatalf("the child, a video within its limit: %d %s", code, body)
	}
	// a hidden video answers exactly as a missing one
	e.same("child", http.MethodGet, "/api/videos/{id}", pfTeen, "")
	if code, _ := e.get("child", "/api/videos/"+pfTeen); code != http.StatusNotFound {
		t.Fatalf("the child, the 16+ video: %d, want 404", code)
	}
	// a sign-in whose device said 9+ (the token's claim, signed by the home) is held
	// to it on the node too
	if _, body := e.get("viewer-device", lists[0]); strings.Contains(body, pfTeenName) || !strings.Contains(body, "Kid-Title") {
		t.Fatalf("the device-limited sign-in's list: %s", body)
	}

	// neither database readable: nobody is shown content, whatever was read before
	// (the cache's short memory of an account with no limit is dropped as a
	// listener that lost its connection drops it)
	copySrv.off.Store(true)
	e.gate.Cache().Invalidate("")
	for _, who := range []string{"child", "viewer", "viewer-device", "guardian", "admin"} {
		for _, path := range append(lists, "/api/videos/"+pfKid) {
			code, body := e.get(who, path)
			if code < 500 {
				t.Fatalf("%s, GET %s with no database: %d %s, want a refusal", who, path, code, body)
			}
			if strings.Contains(body, "Kid-Title") || strings.Contains(body, pfTeenName) {
				t.Fatalf("%s, GET %s with no database shows a video: %s", who, path, body)
			}
		}
	}

	// the copy answers again: the same limits as before
	copySrv.off.Store(false)
	if code, body := e.get("viewer", lists[0]); code != http.StatusOK || !strings.Contains(body, pfTeenName) {
		t.Fatalf("the viewer once the copy answers again: %d %s", code, body)
	}
	if code, body := e.get("child", lists[0]); code != http.StatusOK || strings.Contains(body, pfTeenName) || !strings.Contains(body, "Kid-Title") {
		t.Fatalf("the child once the copy answers again: %d %s", code, body)
	}
}
