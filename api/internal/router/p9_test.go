package router

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg/runtimecfgtest"
)

// The home's name is the stored one, else the computer's (asked once).
func TestHomeName(t *testing.T) {
	db := runtimecfgtest.DB(t)
	asked := 0
	name := HomeName(runtimecfg.New(db), func() string { asked++; return "Sara's Mac mini" })
	ctx := context.Background()
	if got := name(ctx); got != "Sara's Mac mini" {
		t.Fatalf("default = %q", got)
	}
	_ = name(ctx)
	if asked != 1 {
		t.Fatalf("the computer's name was asked %d times", asked)
	}
	runtimecfgtest.Set(t, db, runtimecfg.HomeName, `"Living room"`)
	if got := HomeName(runtimecfg.New(db), nil)(ctx); got != "Living room" {
		t.Fatalf("stored = %q", got)
	}
}
