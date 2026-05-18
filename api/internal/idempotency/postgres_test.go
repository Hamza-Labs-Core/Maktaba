package idempotency

import (
	"context"
	"database/sql"
	"errors"
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
	// rows keyed by composite (userID\x00key).
	rows map[string]Record
	// inserts counts how many Save calls actually inserted a row (vs.
	// lost the ON CONFLICT race). Used to assert race-safety.
	inserts int
}

func newFakeDB() *fakeDB { return &fakeDB{rows: map[string]Record{}} }

func (f *fakeDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case isInsert(query):
		// args: composite, userID, key, requestHash, status, body,
		// headers(json), createdAt
		composite := args[0].(string)
		if _, exists := f.rows[composite]; exists {
			// ON CONFLICT DO NOTHING — 0 rows affected.
			return fakeResult{0}, nil
		}
		f.rows[composite] = Record{
			Key:         args[2].(string),
			UserID:      args[1].(string),
			RequestHash: args[3].(string),
			Status:      args[4].(int),
			Body:        args[5].([]byte),
			Headers:     decodeHeaders(args[6].([]byte)),
			CreatedAt:   args[7].(time.Time),
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
	composite := args[0].(string)
	r, ok := f.rows[composite]
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
