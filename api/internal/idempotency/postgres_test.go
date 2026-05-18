package idempotency

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDB is an in-process stand-in for *sql.DB that records the SQL the
// PostgresStore emits and answers via a goroutine-safe map. It lets the
// unit tier exercise PostgresStore's query construction, ON CONFLICT
// race-safety, and TTL-expiry predicate without a live Postgres (the
// real-DB assertions live in api/migrate_integration_test.go, build tag
// `integration`). Mirrors the interface-seam approach R3 used for the
// ACL store.
type fakeDB struct {
	mu sync.Mutex
	// rows keyed by the in-memory composite of (userID, idem_key).
	// This is a Go-map convenience ONLY — the SQL the store emits now
	// carries user_id and idem_key as two separate parameterised
	// columns; nothing here joins them with a NUL the way the buggy
	// first cut did. errPGNul (below) is what guarantees that.
	rows map[[2]string]Record
	// inserts counts how many Save calls actually inserted a row (vs.
	// lost the ON CONFLICT race). Used to assert race-safety.
	inserts int
}

func newFakeDB() *fakeDB { return &fakeDB{rows: map[[2]string]Record{}} }

// errPGNul is the unit-tier model of the production bug. Postgres
// TEXT/varchar rejects the 0x00 byte with exactly this class of error
// (`pq: invalid byte sequence for encoding "UTF8": 0x00`). The W1-C3
// first cut joined userID + "\x00" + key into one TEXT column, which
// is unstorable on real Postgres but was silently tolerated by the
// old map-keyed-by-string fake — that is precisely how the bug slipped
// review. This fake now rejects a NUL in ANY value bound to a
// TEXT-modelled column, so the regression cannot reappear unnoticed.
var errPGNul = errors.New(`pq: invalid byte sequence for encoding "UTF8": 0x00`)

// rejectNulText fails if s contains a NUL, matching Postgres TEXT
// column behaviour. user_id, idem_key, and request_hash are TEXT.
func rejectNulText(s string) error {
	if strings.IndexByte(s, 0) >= 0 {
		return errPGNul
	}
	return nil
}

func (f *fakeDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case isInsert(query):
		// args: userID, key, requestHash, status, body,
		// headers(json), createdAt
		userID := args[0].(string)
		key := args[1].(string)
		reqHash := args[2].(string)
		// Model Postgres TEXT semantics: a NUL in any TEXT column is a
		// hard error, never a silently-stored byte.
		for _, txt := range []string{userID, key, reqHash} {
			if err := rejectNulText(txt); err != nil {
				return nil, err
			}
		}
		pk := [2]string{userID, key}
		if _, exists := f.rows[pk]; exists {
			// ON CONFLICT (user_id, idem_key) DO NOTHING — 0 rows.
			return fakeResult{0}, nil
		}
		f.rows[pk] = Record{
			Key:         key,
			UserID:      userID,
			RequestHash: reqHash,
			Status:      args[3].(int),
			Body:        args[4].([]byte),
			Headers:     decodeHeaders(args[5].([]byte)),
			CreatedAt:   args[6].(time.Time),
		}
		f.inserts++
		return fakeResult{1}, nil
	case isSweep(query):
		cutoff := args[0].(time.Time)
		n := 0
		for k, r := range f.rows {
			if r.CreatedAt.Before(cutoff) {
				delete(f.rows, k)
				n++
			}
		}
		return fakeResult{int64(n)}, nil
	}
	return nil, errors.New("fakeDB: unexpected exec query: " + query)
}

func (f *fakeDB) QueryRowContext(_ context.Context, _ string, args ...any) rowScanner {
	f.mu.Lock()
	defer f.mu.Unlock()
	// lookupSQL binds (user_id, idem_key).
	userID := args[0].(string)
	key := args[1].(string)
	r, ok := f.rows[[2]string{userID, key}]
	return &fakeRow{rec: r, found: ok}
}

type fakeResult struct{ n int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.n, nil }

type fakeRow struct {
	rec   Record
	found bool
}

func (r *fakeRow) Scan(dest ...any) error {
	if !r.found {
		return sql.ErrNoRows
	}
	*(dest[0].(*string)) = r.rec.RequestHash
	*(dest[1].(*int)) = r.rec.Status
	*(dest[2].(*[]byte)) = r.rec.Body
	*(dest[3].(*[]byte)) = encodeHeaders(r.rec.Headers)
	*(dest[4].(*time.Time)) = r.rec.CreatedAt
	return nil
}

func TestPostgresStore_ReplaySameKey(t *testing.T) {
	db := newFakeDB()
	s := NewPostgresStore(db)
	ctx := context.Background()

	rec := Record{
		Key:         "K1",
		UserID:      "u1",
		RequestHash: "abc",
		Status:      201,
		Body:        []byte(`{"ok":true}`),
		Headers:     map[string]string{"Content-Type": "application/json"},
	}
	if err := s.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := s.Lookup(ctx, "K1", "u1")
	if !ok {
		t.Fatal("Lookup: want hit, got miss")
	}
	if got.Status != 201 || string(got.Body) != `{"ok":true}` || got.RequestHash != "abc" {
		t.Fatalf("Lookup roundtrip mismatch: %+v", got)
	}
	if got.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers not roundtripped: %+v", got.Headers)
	}
}

func TestPostgresStore_DistinctKeysIndependent(t *testing.T) {
	db := newFakeDB()
	s := NewPostgresStore(db)
	ctx := context.Background()

	_ = s.Save(ctx, Record{Key: "A", UserID: "u", RequestHash: "h1", Status: 200, Body: []byte("a")})
	_ = s.Save(ctx, Record{Key: "B", UserID: "u", RequestHash: "h2", Status: 200, Body: []byte("b")})

	a, okA := s.Lookup(ctx, "A", "u")
	b, okB := s.Lookup(ctx, "B", "u")
	if !okA || !okB {
		t.Fatalf("both keys should resolve: A=%v B=%v", okA, okB)
	}
	if string(a.Body) != "a" || string(b.Body) != "b" {
		t.Fatalf("cross-talk between distinct keys: A=%q B=%q", a.Body, b.Body)
	}
	if _, ok := s.Lookup(ctx, "A", "other-user"); ok {
		t.Fatal("same key, different user must not resolve")
	}
}

func TestPostgresStore_ExpiryHonored(t *testing.T) {
	db := newFakeDB()
	s := NewPostgresStore(db)
	ctx := context.Background()

	old := Record{Key: "old", UserID: "u", RequestHash: "h", Status: 200, Body: []byte("x")}
	old.CreatedAt = time.Now().Add(-48 * time.Hour)
	_ = s.Save(ctx, old)
	_ = s.Save(ctx, Record{Key: "fresh", UserID: "u", RequestHash: "h", Status: 200, Body: []byte("y")})

	n, err := s.SweepExpired(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("SweepExpired dropped %d, want 1", n)
	}
	if _, ok := s.Lookup(ctx, "old", "u"); ok {
		t.Fatal("expired record should be gone after sweep")
	}
	if _, ok := s.Lookup(ctx, "fresh", "u"); !ok {
		t.Fatal("fresh record must survive sweep")
	}
}

func TestPostgresStore_ConcurrentDuplicateRaceSafe(t *testing.T) {
	db := newFakeDB()
	s := NewPostgresStore(db)
	ctx := context.Background()

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = s.Save(ctx, Record{
				Key:         "race",
				UserID:      "u",
				RequestHash: "h",
				Status:      200,
				Body:        []byte("body"),
			})
		}(i)
	}
	wg.Wait()

	// ON CONFLICT DO NOTHING ⇒ exactly one writer wins; the rest are
	// no-ops. The single stored record is then replayable.
	if db.inserts != 1 {
		t.Fatalf("concurrent duplicate Save inserted %d rows, want exactly 1", db.inserts)
	}
	if _, ok := s.Lookup(ctx, "race", "u"); !ok {
		t.Fatal("after concurrent dup writes the record must be replayable")
	}
}

func TestPostgresStore_ImplementsStore(t *testing.T) {
	var _ Store = (*PostgresStore)(nil)
}

// TestPostgresStore_NoNulByteWrittenToText is the unit-tier guard for
// the W1-C3 hotfix. The store must never bind a value containing 0x00
// to a Postgres TEXT column. The fake now models real Postgres
// (rejectNulText → errPGNul) so this asserts the production bug
// (`pq: invalid byte sequence for encoding "UTF8": 0x00`) cannot
// recur. user_id and idem_key go in as SEPARATE columns; there is no
// NUL-joined composite anymore.
//
// Fail-without-fix evidence: revert postgres.go to the old
// `compositeKey(r.Key, r.UserID)` (userID + "\x00" + key) bound to the
// first TEXT column and this test fails with errPGNul on Save — which
// is exactly the prod/CI failure. (Demonstrated below by calling the
// old compositeKey join through the same fake.)
func TestPostgresStore_NoNulByteWrittenToText(t *testing.T) {
	db := newFakeDB()
	s := NewPostgresStore(db)
	ctx := context.Background()

	// Realistic values: a UUID-ish user and an opaque client key.
	rec := Record{
		Key:         "client-supplied-idem-key-7f3a",
		UserID:      "11111111-2222-3333-4444-555555555555",
		RequestHash: "deadbeef",
		Status:      201,
		Body:        []byte(`{"ok":true}`),
		Headers:     map[string]string{"Content-Type": "application/json"},
	}
	if err := s.Save(ctx, rec); err != nil {
		t.Fatalf("Save must not write a NUL to a TEXT column; got %v", err)
	}
	if _, ok := s.Lookup(ctx, rec.Key, rec.UserID); !ok {
		t.Fatal("record must round-trip after NUL-free Save")
	}

	// Prove the guard has teeth: the OLD design joined userID + \x00 +
	// key into one TEXT column. Feed that exact string through the
	// fake's TEXT-column model and it must be rejected the way real
	// Postgres rejects it. If this ever stops failing, the guard is
	// toothless and the bug could silently return.
	oldComposite := compositeKey(rec.Key, rec.UserID) // userID + "\x00" + key
	if err := rejectNulText(oldComposite); err == nil {
		t.Fatal("guard is toothless: the old NUL-joined composite was NOT rejected")
	} else if !errors.Is(err, errPGNul) {
		t.Fatalf("old composite rejected with unexpected error: %v", err)
	}
}

// TestPostgresStore_RequestHashConflictContractPreserved pins the
// middleware's 409 contract end-to-end through the store: a second
// request with the SAME (user_id, idem_key) but a DIFFERENT body must
// still expose the FIRST writer's RequestHash on Lookup (ON CONFLICT
// DO NOTHING keeps the winner), so the middleware can compare hashes
// and emit 409 on mismatch. The two-column key change must not alter
// this.
func TestPostgresStore_RequestHashConflictContractPreserved(t *testing.T) {
	db := newFakeDB()
	s := NewPostgresStore(db)
	ctx := context.Background()

	first := Record{Key: "K", UserID: "u", RequestHash: "hash-A", Status: 201, Body: []byte("A")}
	if err := s.Save(ctx, first); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	// A retry that changed its body: same key, different hash. ON
	// CONFLICT DO NOTHING ⇒ this is a no-op; winner's row is untouched.
	second := Record{Key: "K", UserID: "u", RequestHash: "hash-B", Status: 200, Body: []byte("B")}
	if err := s.Save(ctx, second); err != nil {
		t.Fatalf("Save second (conflict): %v", err)
	}

	got, ok := s.Lookup(ctx, "K", "u")
	if !ok {
		t.Fatal("Lookup miss after conflicting writes")
	}
	if got.RequestHash != "hash-A" {
		t.Fatalf("409-guard contract broken: stored RequestHash = %q, want first writer's %q",
			got.RequestHash, "hash-A")
	}
	if string(got.Body) != "A" {
		t.Fatalf("winner's body must survive the conflicting retry, got %q", got.Body)
	}
}

// errRow is a rowScanner whose Scan always fails with a non-ErrNoRows
// error — a real DB failure (connection reset, perms), as opposed to
// the benign "no such row".
type errRow struct{ err error }

func (r errRow) Scan(_ ...any) error { return r.err }

// errDB returns errRow from every QueryRowContext so Lookup hits the
// non-ErrNoRows branch. Save/Sweep are unused here.
type errDB struct{ err error }

func (d errDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, d.err
}

func (d errDB) QueryRowContext(_ context.Context, _ string, _ ...any) rowScanner {
	return errRow{err: d.err}
}

// TestPostgresStore_LookupDBError_MissAndWarn pins Fix #1: a real
// (non-ErrNoRows) Lookup failure must STILL be a miss (the
// in-memory-equivalent contract is unchanged) but must no longer be
// silent — it emits a Warn breadcrumb so a persistently failing replay
// path that silently re-executes mutations is observable.
//
// Fails without the fix: pre-fix Lookup collapsed every error into a
// bare `return Record{}, false` with no log, so the captured buffer
// would be empty and the WARN assertion would fail.
func TestPostgresStore_LookupDBError_MissAndWarn(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(h)

	s := NewPostgresStoreDB(nil, logger)
	// Swap in the failing executor (NewPostgresStoreDB wraps a real
	// *sql.DB; we only need the dbExecutor seam for this unit).
	s.db = errDB{err: errors.New("connection reset by peer")}

	rec, ok := s.Lookup(context.Background(), "K1", "u1")
	if ok {
		t.Fatalf("Lookup on DB error must be a miss, got hit: %+v", rec)
	}
	if rec.Key != "" || rec.Status != 0 || rec.Body != nil || rec.RequestHash != "" {
		t.Fatalf("Lookup on DB error must return zero Record, got %+v", rec)
	}

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("expected a WARN breadcrumb on DB-error-as-miss, log was: %q", out)
	}
	if !strings.Contains(out, "idempotency_lookup_failed") {
		t.Fatalf("expected event=idempotency_lookup_failed in log, log was: %q", out)
	}
	if !strings.Contains(out, "connection reset by peer") {
		t.Fatalf("expected the underlying err in the log, log was: %q", out)
	}
}

// TestPostgresStore_LookupNoRows_SilentMiss pins the other half of Fix
// #1: a benign sql.ErrNoRows (or no row) is a normal miss and must NOT
// log — otherwise every cache-miss would spam Warn.
func TestPostgresStore_LookupNoRows_SilentMiss(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	s := NewPostgresStoreDB(nil, logger)
	s.db = newFakeDB() // empty → fakeRow returns sql.ErrNoRows

	if _, ok := s.Lookup(context.Background(), "absent", "u"); ok {
		t.Fatal("Lookup on absent key must be a miss")
	}
	if buf.Len() != 0 {
		t.Fatalf("a plain cache-miss (ErrNoRows) must not log, got: %q", buf.String())
	}
}
