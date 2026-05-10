package perf

import (
	"database/sql"
	"testing"
	"time"
)

func TestApplyPoolRejectsBadConfig(t *testing.T) {
	cases := []PoolConfig{
		{MaxOpen: 0},
		{MaxOpen: 10, MaxIdle: 20},
		{MaxOpen: 10, MaxIdle: -1},
	}
	for i, c := range cases {
		if err := ApplyPool(&sql.DB{}, c); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}

func TestApplyPoolRejectsNilDB(t *testing.T) {
	if err := ApplyPool(nil, DefaultPoolConfig()); err == nil {
		t.Fatal("expected error on nil db")
	}
}

func TestDefaultPoolConfigSane(t *testing.T) {
	c := DefaultPoolConfig()
	if c.MaxOpen < 10 {
		t.Fatalf("MaxOpen too low: %d", c.MaxOpen)
	}
	if c.MaxIdle > c.MaxOpen {
		t.Fatalf("MaxIdle %d > MaxOpen %d", c.MaxIdle, c.MaxOpen)
	}
	if c.MaxLifetime <= c.MaxIdleTime {
		t.Fatalf("MaxLifetime should exceed MaxIdleTime")
	}
	if c.ConnTimeout < time.Second {
		t.Fatalf("ConnTimeout too short: %v", c.ConnTimeout)
	}
}
