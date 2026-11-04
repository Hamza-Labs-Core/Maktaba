package pgpool

import (
	"context"
	"testing"
)

const dsn = "postgres://maktaba@127.0.0.1:1/maktaba?sslmode=disable"

func TestConfigCapsThePool(t *testing.T) {
	for _, n := range []int32{Serve, Slot, Copy} {
		cfg, err := Config(dsn, n)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxConns != n || cfg.MinConns != 0 || cfg.MaxConnIdleTime != IdleTime {
			t.Fatalf("Config(%d): MaxConns %d MinConns %d idle %v", n, cfg.MaxConns, cfg.MinConns, cfg.MaxConnIdleTime)
		}
	}
}

// A dsn's own pool_max_conns is replaced too: the cap is the service's, not the URL's.
func TestConfigOverridesTheURLsSize(t *testing.T) {
	cfg, err := Config(dsn+"&pool_max_conns=50", Slot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != Slot {
		t.Fatalf("MaxConns = %d, want %d", cfg.MaxConns, Slot)
	}
}

func TestConfigRefusesAnEmptyPool(t *testing.T) {
	if _, err := Config(dsn, 0); err == nil {
		t.Fatal("a pool of 0 connections was accepted")
	}
}

// New opens nothing until a query: a home that does not answer yet still gets its pool.
func TestNewIsLazyAndCapped(t *testing.T) {
	pool, err := New(context.Background(), dsn, Serve)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if got := pool.Config().MaxConns; got != Serve {
		t.Fatalf("MaxConns = %d, want %d", got, Serve)
	}
	if st := pool.Stat(); st.TotalConns() != 0 {
		t.Fatalf("New opened %d connections before any query", st.TotalConns())
	}
}

func TestNewRefusesABadURL(t *testing.T) {
	if _, err := New(context.Background(), "postgres://%zz", Serve); err == nil {
		t.Fatal("a bad URL opened a pool")
	}
}
