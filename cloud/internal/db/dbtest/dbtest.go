// Package dbtest gives an integration test a migrated cloud database of
// its own (MAKTABA_CLOUD_DB_URL names the server): CI's shared database
// already holds the home api's tables (its users among them), which the
// cloud's migrations must not meet.
//
// With MAKTABA_CLOUD_DB_SESSION_URL also set (relay.yml's PgBouncer run, story
// 50.18), MAKTABA_CLOUD_DB_URL is PgBouncer's transaction pool and the session
// URL its session pool: the URL MigratedURL returns, for what holds a session of
// its own (LISTEN, a session lock), goes through the session pool.
package dbtest

import (
	"context"
	neturl "net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

var seq atomic.Int64

// Migrated makes a scratch database, migrates it and drops it after the
// test; it skips the test when MAKTABA_CLOUD_DB_URL is unset.
func Migrated(t *testing.T) *db.Pool {
	pool, _ := MigratedURL(t)
	return pool
}

// SessionEnv names the server for sessions of their own (a PgBouncer session pool);
// empty: MAKTABA_CLOUD_DB_URL's.
const SessionEnv = "MAKTABA_CLOUD_DB_SESSION_URL"

// SessionURL is url's database on SessionEnv's server, or url when SessionEnv is unset.
func SessionURL(t *testing.T, url string) string {
	t.Helper()
	out, err := sessionURL(url, os.Getenv(SessionEnv))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sessionURL puts dbURL's database (its path) on sessionBase's server and credentials.
func sessionURL(dbURL, sessionBase string) (string, error) {
	if sessionBase == "" {
		return dbURL, nil
	}
	d, err := neturl.Parse(dbURL)
	if err != nil {
		return "", err
	}
	s, err := neturl.Parse(sessionBase)
	if err != nil {
		return "", err
	}
	s.Path = d.Path
	return s.String(), nil
}

// MigratedURL is Migrated, and the scratch database's URL for a session of its
// own (a second pool, a listener or a Cut; SessionEnv's server when set).
func MigratedURL(t *testing.T) (*db.Pool, string) {
	t.Helper()
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := db.Open(url, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_t_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.FormatInt(seq.Add(1), 10)
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
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
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool, SessionURL(t, u.String())
}
