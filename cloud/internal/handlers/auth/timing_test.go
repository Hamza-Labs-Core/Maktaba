package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// madeAtInit is how often the package's init made the timing hash: TestMain reads it after
// init and before any test runs.
var madeAtInit int32

func TestMain(m *testing.M) {
	madeAtInit = dummyHashMade.Load()
	os.Exit(m.Run())
}

// The timing hash takes 64 MiB of argon2id; the package's init must not make it, or every role
// of the multi-role cloud binary (the relay too) holds that memory from its start.
func TestInitDoesNotMakeTheTimingHash(t *testing.T) {
	if madeAtInit != 0 {
		t.Fatalf("the timing hash was made %d time(s) at init", madeAtInit)
	}
}

// The timing hash is a real argon2id hash of its phrase, made once.
func TestTheTimingHashIsMadeOnce(t *testing.T) {
	h := dummyHash()
	if h != dummyHash() || dummyHashMade.Load() != 1 {
		t.Fatalf("made %d times", dummyHashMade.Load())
	}
	if argon2id.Verify("maktaba-timing-equaliser", h) != nil || argon2id.Verify("another", h) == nil {
		t.Fatal("not a hash of its phrase")
	}
}

// A sign-in for an unknown email still spends an argon2id verification, so its answer takes as
// long as a wrong password's and does not tell whether the account exists.
func TestAnUnknownEmailStillSpendsTheVerifyTime(t *testing.T) {
	d := &Deps{Users: stores.NewUsers(sql.OpenDB(noRows{})), Logger: slog.New(slog.DiscardHandler)}
	login := func() time.Duration {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login",
			strings.NewReader(`{"email":"nobody@example.com","password":"wrong-password"}`))
		start := time.Now()
		d.Login(rec, req)
		took := time.Since(start)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid_credentials") {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		return took
	}
	login() // the first sign-in also makes the timing hash
	if n := dummyHashMade.Load(); n != 1 {
		t.Fatalf("an unknown email did not use the timing hash (made %d times)", n)
	}

	known, _ := argon2id.Hash("a-real-password", argon2id.DefaultParams())
	verify := func() time.Duration {
		start := time.Now()
		_ = argon2id.Verify("wrong-password", known)
		return time.Since(start)
	}
	// The fastest of a few runs on each side: noise only ever adds time.
	fastest := func(f func() time.Duration) time.Duration {
		best := f()
		for range 2 {
			best = min(best, f())
		}
		return best
	}
	miss, wrong := fastest(login), fastest(verify)
	if miss < wrong/2 {
		t.Fatalf("an unknown email took %v, a wrong password's verification %v", miss, wrong)
	}
}

// noRows is a database/sql driver whose every query returns no rows: Users.ByEmail then answers
// ErrUserNotFound with no database.
type noRows struct{}

func (noRows) Connect(context.Context) (driver.Conn, error) { return noRows{}, nil }
func (noRows) Driver() driver.Driver                        { return nil }
func (noRows) Prepare(string) (driver.Stmt, error)          { return noRows{}, nil }
func (noRows) Close() error                                 { return nil }
func (noRows) Begin() (driver.Tx, error)                    { return nil, driver.ErrSkip }
func (noRows) NumInput() int                                { return -1 }
func (noRows) Exec([]driver.Value) (driver.Result, error)   { return driver.RowsAffected(0), nil }
func (noRows) Query([]driver.Value) (driver.Rows, error)    { return emptyRows{}, nil }

type emptyRows struct{}

func (emptyRows) Columns() []string         { return nil }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }
