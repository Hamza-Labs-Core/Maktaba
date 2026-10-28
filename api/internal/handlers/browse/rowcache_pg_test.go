package browse

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The row cache's tests run on a scratch Postgres (slot 0167's trigger is
// what invalidates), only with MAKTABA_TEST_PG_URL.

type rowCacheEnv struct {
	*env
	dsn   string
	h     *Handler
	c     *RowCache
	clock atomic.Int64 // seconds past now
	n     int
}

func newRowCacheEnv(t *testing.T) *rowCacheEnv {
	t.Helper()
	db, dsn := clustertest.ScratchDB(t)
	for _, q := range []string{
		`INSERT INTO users (id, username, pw_hash) VALUES ('` + viewer + `', 'rc-viewer', 'x'), ('` + adminID + `', 'rc-admin', 'x')`,
		`INSERT INTO libraries (id, name, roots) VALUES ('` + libA + `', 'Alpha', '{/m/a}'), ('` + libB + `', 'Beta', '{/m/b}'),
			('` + libC + `', 'Gamma', '{/m/c}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := &rowCacheEnv{dsn: dsn, c: NewRowCache(ctx)}
	e.c.now = func() time.Time { return now.Add(time.Duration(e.clock.Load()) * time.Second) }
	e.h = &Handler{DB: db, NowFunc: func() time.Time { return now }, Rows: e.c}
	r := chi.NewRouter()
	e.h.Mount(r)
	e.env = &env{t: t, db: db, r: r}
	return e
}

// add inserts a ready video in lib, newer than every one before it.
func (e *rowCacheEnv) add(db interface {
	Exec(string, ...any) (sql.Result, error)
}, lib string) string {
	e.t.Helper()
	e.n++
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", 900+e.n)
	at := now.Add(time.Duration(e.n) * time.Minute)
	if _, err := db.Exec(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, state, title, created_at)
		VALUES ($1,$2,$3,$4,$5,1,$6,'ready',$5,$6)`, id, lib, fmt.Sprintf("%064x", 900+e.n), "/m/"+id, fmt.Sprintf("v%d.mp4", e.n), at); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// get is lib's row of n through the cache.
func (e *rowCacheEnv) get(lib string, n int, load rowLoader) []Card {
	e.t.Helper()
	m, err := e.c.rows(e.t.Context(), []string{lib}, n, load)
	if err != nil {
		e.t.Fatal(err)
	}
	return m[lib]
}

func (e *rowCacheEnv) reads() int {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	return e.c.reads
}

// eventually waits (at most 5 s) for ok.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(end) {
			t.Fatalf("%s: not within 5 s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A cached row is served at once; past the freshness window it is still
// served at once while one background read runs, and the next request has
// the new row.
func TestRowCacheServesWhileRefreshingPostgres(t *testing.T) {
	e := newRowCacheEnv(t)
	e.c.Resume() // no listener here: only the window refreshes
	first := e.add(e.db, libA)
	var loads atomic.Int32
	gate := make(chan struct{})
	var gated atomic.Bool
	load := func(ctx context.Context, libs []string, n int) (map[string][]Card, error) {
		loads.Add(1)
		if gated.Load() {
			<-gate
		}
		return e.h.recentPerLibrary(ctx, libs, n)
	}

	if got := ids(e.get(libA, RowSize, load)); !slices.Equal(got, []string{first}) || loads.Load() != 1 {
		t.Fatalf("cold read: %v after %d reads", got, loads.Load())
	}
	second := e.add(e.db, libA)
	e.clock.Store(10) // within the window: the entry, no read
	if got := ids(e.get(libA, RowSize, load)); !slices.Equal(got, []string{first}) || loads.Load() != 1 {
		t.Fatalf("fresh entry: %v after %d reads", got, loads.Load())
	}

	e.clock.Store(31) // past it: the entry at once, one read behind it
	gated.Store(true)
	for range 5 {
		if got := ids(e.get(libA, RowSize, load)); !slices.Equal(got, []string{first}) {
			t.Fatalf("while refreshing: %v, want the cached row", got)
		}
	}
	eventually(t, "the background read starts", func() bool { return loads.Load() == 2 })
	close(gate)
	eventually(t, "the background read ends", func() bool { return e.reads() == 2 })
	if got := ids(e.get(libA, RowSize, load)); !slices.Equal(got, []string{second, first}) || loads.Load() != 2 {
		t.Fatalf("after the refresh: %v after %d reads, want the new row and no other read", got, loads.Load())
	}
}

// Callers wanting a row not cached share one read.
func TestRowCacheColdReadSingleFlightPostgres(t *testing.T) {
	e := newRowCacheEnv(t)
	e.c.Resume()
	want := e.add(e.db, libA)
	var loads atomic.Int32
	gate := make(chan struct{})
	load := func(ctx context.Context, libs []string, n int) (map[string][]Card, error) {
		loads.Add(1)
		<-gate
		return e.h.recentPerLibrary(ctx, libs, n)
	}
	var wg sync.WaitGroup
	got := make([][]string, 20)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := e.c.rows(context.Background(), []string{libA}, RowSize, load)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = ids(m[libA])
		}()
	}
	eventually(t, "the read starts", func() bool { return loads.Load() == 1 })
	time.Sleep(20 * time.Millisecond) // the others reach the entry and wait
	close(gate)
	wg.Wait()
	if loads.Load() != 1 {
		t.Errorf("%d reads for 20 callers, want 1", loads.Load())
	}
	for i, g := range got {
		if !slices.Equal(g, []string{want}) {
			t.Errorf("caller %d: %v", i, g)
		}
	}
}

// Every change of a library's videos drops its entries, whoever writes it:
// slot 0167's trigger notifies, Listen hears. A change elsewhere, or of a
// column no card shows, keeps them.
func TestRowCacheInvalidationPostgres(t *testing.T) {
	e := newRowCacheEnv(t)
	go e.c.Listen(e.t.Context(), e.dsn, nil, nil)
	eventually(t, "the listener connects", func() bool { e.c.mu.Lock(); defer e.c.mu.Unlock(); return e.c.on })
	load := e.h.recentPerLibrary
	a1 := e.add(e.db, libA)
	b1 := e.add(e.db, libB)
	row := func(lib string) []string { return ids(e.get(lib, RowSize, load)) }
	cached := func(lib string) bool {
		e.c.mu.Lock()
		defer e.c.mu.Unlock()
		return e.c.entries[rowKey{lib, RowSize}] != nil
	}
	fill := func() {
		t.Helper()
		row(libA)
		row(libB)
		if !cached(libA) || !cached(libB) {
			t.Fatal("not cached")
		}
	}
	// waitFor waits until lib's entry is gone, then checks its row; the other
	// library's entry stays
	waitFor := func(what, lib, other string, want ...string) {
		t.Helper()
		eventually(t, what, func() bool { return !cached(lib) })
		if other != "" && !cached(other) {
			t.Errorf("%s: %s's entry dropped too", what, other)
		}
		if got := row(lib); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", what, got, want)
		}
	}

	// a scan: several videos in one transaction
	fill()
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	a2, a3 := e.add(tx, libA), e.add(tx, libA)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	waitFor("a scan's videos", libA, libB, a3, a2, a1)

	fill()
	if _, err := e.db.Exec(`UPDATE videos SET title = 'Renamed' WHERE id = $1`, a3); err != nil {
		t.Fatal(err)
	}
	waitFor("a title edited", libA, libB, a3, a2, a1)

	fill()
	if _, err := e.db.Exec(`UPDATE videos SET state = 'missing' WHERE id = $1`, a2); err != nil {
		t.Fatal(err)
	}
	waitFor("a video gone missing", libA, libB, a3, a1)

	fill()
	if _, err := e.db.Exec(`UPDATE videos SET deleted_at = now() WHERE id = $1`, a3); err != nil {
		t.Fatal(err)
	}
	waitFor("a video deleted (soft)", libA, libB, a1)

	// a column no card shows: kept
	fill()
	if _, err := e.db.Exec(`UPDATE videos SET metadata = '{"x":1}', updated_at = now() WHERE id = $1`, a1); err != nil {
		t.Fatal(err)
	}
	// a change in C (not cached) goes through the listener after it: once C's
	// row is new, the metadata change (earlier) has been heard too
	row(libC)
	c1 := e.add(e.db, libC)
	eventually(t, "C's change", func() bool { return !cached(libC) })
	if got := row(libC); !slices.Equal(got, []string{c1}) {
		t.Errorf("C: %v", got)
	}
	if !cached(libA) || !cached(libB) {
		t.Error("a metadata change dropped an entry")
	}

	// a merge moves B's video into A: both libraries
	if _, err := e.db.Exec(`UPDATE videos SET library_id = $1 WHERE id = $2`, libA, b1); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a merge", func() bool { return !cached(libA) && !cached(libB) })
	if got := row(libB); len(got) != 0 {
		t.Errorf("B after the merge: %v", got)
	}

	// a hard delete, and a library removed (its videos go with it)
	fill()
	if _, err := e.db.Exec(`DELETE FROM videos WHERE id = $1`, a1); err != nil {
		t.Fatal(err)
	}
	waitFor("a video deleted", libA, libB, b1)
	fill()
	if _, err := e.db.Exec(`DELETE FROM libraries WHERE id = $1`, libA); err != nil {
		t.Fatal(err)
	}
	waitFor("a library removed", libA, libB)

	// the API's answer follows: a video added shows on the home's pinned row
	// and the hub at the next request
	if _, err := e.db.Exec(`INSERT INTO user_library_pins VALUES ($1,$2,0,now())`, adminID, libB); err != nil {
		t.Fatal(err)
	}
	var home struct{ Pinned []PinnedRows }
	e.do(asAdmin(), "GET", "/api/home", nil, &home)
	b2 := e.add(e.db, libB)
	eventually(t, "the home's row", func() bool {
		e.do(asAdmin(), "GET", "/api/home", nil, &home)
		return len(home.Pinned) == 1 && slices.Equal(ids(home.Pinned[0].RecentlyAdded), []string{b2})
	})
	var hub struct {
		Rows struct {
			Added []Card `json:"recently_added"`
		} `json:"rows"`
	}
	if e.do(asAdmin(), "GET", "/api/libraries/"+libB+"/hub", nil, &hub); !slices.Equal(ids(hub.Rows.Added), []string{b2}) {
		t.Errorf("hub: %v", ids(hub.Rows.Added))
	}

	// the listener gone: nothing is kept, the rows come from the database
	e.c.Suspend()
	before := e.reads()
	row(libB)
	row(libB)
	if e.reads() != before || cached(libB) {
		t.Error("a suspended cache kept a row")
	}
}

// Callers who may see different libraries never share an entry: access is
// checked before the cache, and an entry holds no caller's fields.
func TestRowCacheVisibilityPostgres(t *testing.T) {
	e := newRowCacheEnv(t)
	e.c.Resume()
	a1, b1 := e.add(e.db, libA), e.add(e.db, libB)
	// the admin's playback of a1: never on the pinned row's cards
	if _, err := e.db.Exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at)
		VALUES ($1,$2,100,false,now())`, adminID, a1); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{adminID, viewer} {
		if _, err := e.db.Exec(`INSERT INTO user_library_pins VALUES ($1,$2,0,now()), ($1,$3,1,now())`, uid, libA, libB); err != nil {
			t.Fatal(err)
		}
	}
	// the admin fills both libraries' entries
	var home struct{ Pinned []PinnedRows }
	if e.do(asAdmin(), "GET", "/api/home", nil, &home); len(home.Pinned) != 2 {
		t.Fatalf("admin's home: %d pinned rows", len(home.Pinned))
	}
	var hub map[string]any
	e.do(asAdmin(), "GET", "/api/libraries/"+libB+"/hub", nil, &hub)

	// the viewer (library A only) never sees B's, cached or not
	if e.do(asViewer(), "GET", "/api/home", nil, &home); len(home.Pinned) != 1 || home.Pinned[0].Library.ID != libA ||
		!slices.Equal(ids(home.Pinned[0].RecentlyAdded), []string{a1}) {
		t.Fatalf("viewer's home: %+v", home.Pinned)
	}
	if c := home.Pinned[0].RecentlyAdded[0]; c.PositionSec != nil || c.Completed != nil {
		t.Errorf("viewer's card carries playback: %+v", c)
	}
	if code := e.do(asViewer(), "GET", "/api/libraries/"+libB+"/hub", nil, nil); code != 404 {
		t.Errorf("viewer's hub of B: %d, want 404", code)
	}
	// and the admin still has both
	if e.do(asAdmin(), "GET", "/api/home", nil, &home); len(home.Pinned) != 2 ||
		!slices.Equal(ids(home.Pinned[1].RecentlyAdded), []string{b1}) {
		t.Errorf("admin's home: %+v", home.Pinned)
	}
}

// The entries are bounded: past the cap the least recently used goes.
func TestRowCacheLRUPostgres(t *testing.T) {
	e := newRowCacheEnv(t)
	e.c.Resume()
	e.c.max = 2
	e.add(e.db, libA)
	e.add(e.db, libB)
	e.add(e.db, libC)
	load := e.h.recentPerLibrary
	e.get(libA, RowSize, load)
	e.get(libB, RowSize, load)
	e.get(libA, RowSize, load) // A is now the most recently used
	e.get(libC, RowSize, load)
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	if e.c.lru.Len() != 2 || e.c.entries[rowKey{libB, RowSize}] != nil || e.c.entries[rowKey{libA, RowSize}] == nil {
		t.Errorf("%d entries, B kept %v", e.c.lru.Len(), e.c.entries[rowKey{libB, RowSize}] != nil)
	}
}
