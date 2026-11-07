package main

import (
	"context"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/logs"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
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

// Epic 53 story 53.10: the errors notifier main declares is a loop of the log index, so it
// stops when logs.index is turned off.
func TestErrorsNotifierIsALoopOfTheLogIndex(t *testing.T) {
	c, ok := components.Default().ByLoop("api", "errors_notifier")
	if !ok || c.ID != "logs.index" {
		t.Fatalf("api:errors_notifier belongs to %v (%v), want logs.index", c, ok)
	}
}

// Epic 53 stories 53.10 and 53.15: main declares the errors notifier and gives the problem
// reports the notifications hub.
func TestWireLogNotices(t *testing.T) {
	loops := runner.New(runner.Options{Host: "api", Machine: "home"})
	hub := notify.NewHub(nil, nil)
	t.Cleanup(func() { logs.SetReportNotifier(nil) })
	wireLogNotices(loops, hub, nil)
	if !slices.Contains(loops.Declared(), "errors_notifier") {
		t.Fatalf("declared %v", loops.Declared())
	}
	if logs.ReportNotifier() != hub {
		t.Fatal("the reports have no notifier")
	}
}
