package main

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Epic 53 story 53.01: once the logger is set up, a component loop's context names its
// catalog component, so the loop's lines carry it.
func TestInitLoggerTagsComponentLoops(t *testing.T) {
	saved := runner.LoopContext
	t.Cleanup(func() { runner.LoopContext = saved })
	_ = initLogger()
	if runner.LoopContext == nil {
		t.Fatal("initLogger must set runner.LoopContext")
	}
	ctx := runner.LoopContext(context.Background(), "core.updates")
	if got := mlog.ComponentFrom(ctx); got != "core.updates" {
		t.Fatalf("component = %q, want core.updates", got)
	}
}
