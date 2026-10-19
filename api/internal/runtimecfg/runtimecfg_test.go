package runtimecfg_test

import (
	"context"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg/runtimecfgtest"
)

func TestReader(t *testing.T) {
	ctx := context.Background()
	db := runtimecfgtest.DB(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	r := &runtimecfg.Reader{DB: db, Now: func() time.Time { return now }}
	if _, ok := r.Float(ctx, runtimecfg.SearchFTSWeight); ok {
		t.Fatal("an unset key reads as set")
	}
	runtimecfgtest.Set(t, db, runtimecfg.SearchFTSWeight, `0.25`)
	// cached: the miss stays a miss within the TTL, Invalidate re-reads
	if _, ok := r.Float(ctx, runtimecfg.SearchFTSWeight); ok {
		t.Fatal("the cache was not used")
	}
	r.Invalidate()
	if f, ok := r.Float(ctx, runtimecfg.SearchFTSWeight); !ok || f != 0.25 {
		t.Fatalf("after Invalidate %v %v", f, ok)
	}
	runtimecfgtest.Set(t, db, runtimecfg.SessionURLTTL, `"900"`) // a numeric string
	now = now.Add(runtimecfg.DefaultTTL)
	if d, ok := r.Seconds(ctx, runtimecfg.SessionURLTTL); !ok || d != 15*time.Minute {
		t.Fatalf("seconds %v %v", d, ok)
	}
	for _, bad := range []string{`-1`, `0`, `"soon"`, `true`} {
		runtimecfgtest.Set(t, db, runtimecfg.WSHeartbeat, bad)
		r.Invalidate()
		if d, ok := r.Seconds(ctx, runtimecfg.WSHeartbeat); ok {
			t.Errorf("%s read as %v", bad, d)
		}
	}
	var nilReader *runtimecfg.Reader
	if _, ok := nilReader.Float(ctx, runtimecfg.SearchFTSWeight); ok {
		t.Fatal("a nil reader read a value")
	}
	nilReader.Invalidate()
}
