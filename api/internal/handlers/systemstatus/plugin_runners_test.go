package systemstatus

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Epic 54: a first-party runner that failed is attention, with the stages set to run as
// plugins in its group (they run in the worker meanwhile); a held or idle runner and the
// stages still in-process are not counted. The detail survives the trip through JSON.
func TestPluginRunnersLine(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	plugin := []string{"books", "detect", "tag", "transcribe_plugin_only"}
	run := func(rows []store.Status, stagesErr error) Item {
		h := &Handler{Components: fakeComponents{rows: rows}, Now: func() time.Time { return now },
			PluginStages: func(context.Context) ([]string, error) { return plugin, stagesErr }}
		return only(t, checkPluginRunners(context.Background(), newEnv(h)))
	}
	runners := func(machine string, phase string, failed any) store.Status {
		r := compRow(machine, "core.plugin_runners", "supervisor", phase, "", now.Add(-time.Hour))
		if failed != nil {
			r.Detail = map[string]any{"failed_runners": failed}
		}
		return r
	}

	calm := run([]store.Status{runners("home", "running", nil)}, nil)
	want(t, calm, StatusOK, "plugin_runners_ok")
	if calm.Attention || calm.Link != "/settings/plugins" {
		t.Fatalf("calm: %+v", calm)
	}

	// the light runner failed on a node, the net runner at home (as JSON reads it back)
	got := run([]store.Status{
		runners("node-b", "failed", []string{"light"}),
		runners("home", "failed", []any{"net"}),
		compRow("home", "transcription", "supervisor", "running", "", now),
	}, nil)
	want(t, got, StatusDegraded, "plugin_runners_failed")
	if !got.Attention || got.Params["failed"] != 2 || got.Params["machine"] != "home" ||
		got.Params["runner"] != "net" || got.Params["stages"] != 1 {
		t.Fatalf("failed: %+v", got)
	}
	issues, _ := got.Details["failed"].([]RunnerIssue)
	if len(issues) != 2 || !slices.Equal(issues[0].Stages, []string{"books"}) ||
		issues[1].Machine != "node-b" || !slices.Equal(issues[1].Stages, []string{"detect", "tag"}) {
		t.Fatalf("issues: %+v", issues)
	}
	if got.Reason != "2 failed: the net runner on home. Its stages set to run as plugins (1) run in the worker, "+
		"outside their sandbox, until it runs again." {
		t.Fatalf("reason: %q", got.Reason)
	}

	// a slotted runner (ml-3) is its group's; stages unreadable: the runner is still named
	ml := run([]store.Status{runners("home", "failed", []string{"ml-3"})}, errors.New("no table"))
	want(t, ml, StatusDegraded, "plugin_runners_failed")
	if ml.Params["runner"] != "ml-3" || ml.Params["stages"] != 0 {
		t.Fatalf("ml: %+v", ml)
	}

	// unreadable component health
	h := &Handler{Components: fakeComponents{err: errors.New("down")}, Now: func() time.Time { return now }}
	want(t, only(t, checkPluginRunners(context.Background(), newEnv(h))), StatusDegraded, "plugin_runners_unreadable")
}
