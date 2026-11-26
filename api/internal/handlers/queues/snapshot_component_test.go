package queues

import (
	"database/sql/driver"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// T14 (Epic 51, story 51.06): a stage its component switched off or held waits with
// that reason and names the component; an operator's switch stays "disabled".
func TestSummarizeStage_ComponentReasons(t *testing.T) {
	ptr := func(s string) *string { return &s }
	tests := []struct {
		name      string
		stage     string
		reason    string
		want      WaitingReason
		component string
	}{
		{"off", "transcribe", "component_off", WaitComponentOff, "transcription"},
		{"held", "thumbnail", "component_held", WaitComponentHeld, "media.thumbnails"},
		{"removed maktaba plugin", "thumb_small", "component_off", WaitComponentOff, "media.thumbnails"},
		{"operator", "transcribe", "operator", WaitDisabled, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := stageInputs{
				Info:     StageInfo{Stage: tc.stage, Enabled: false, DisabledReason: ptr(tc.reason)},
				Live:     liveCounts{Pending: 3},
				PoolSize: 2, Now: t0, Served: Coverage{Workers: 1},
			}
			s := summarizeStage(in)
			if s.WaitingReason == nil || *s.WaitingReason != tc.want {
				t.Fatalf("waiting reason %v, want %v", s.WaitingReason, tc.want)
			}
			if tc.component == "" {
				if s.Component != nil {
					t.Fatalf("component %q, want none", *s.Component)
				}
				return
			}
			if s.Component == nil || *s.Component != tc.component {
				t.Fatalf("component %v, want %q", s.Component, tc.component)
			}
		})
	}
}

// Story 54.46 (#1463 item 1): GET /api/queues shows parked jobs of a removed Maktaba
// plugin with waiting_reason component_off and the owning component.
func TestSnapshot_RemovedMaktabaPluginShowsComponentOff(t *testing.T) {
	f := newFixture(
		stagesRule(stageRow("thumb_small", "media", 1, "cpu", withDisabled("component_off"))),
		&queuestest.Rule{Match: "from worker_pools", Cols: []string{"pool", "label", "scope", "size", "default_size", "updated_at", "updated_by"},
			Rows: [][]driver.Value{{"cpu", "CPU", "per_worker", nil, nil, t0, nil}}},
		&queuestest.Rule{Match: "from queue_control", Cols: []string{"paused"}, Rows: [][]driver.Value{{false}}},
		&queuestest.Rule{Match: "min(created_at) filter", Cols: []string{"stage", "pending", "ready", "backoff", "running", "paused", "since", "oldest", "backoff_cause", "quiet", "quiet_at"},
			Rows: [][]driver.Value{{"thumb_small", int64(2), int64(2), int64(0), int64(0), int64(0), t0.Add(-time.Hour), t0.Add(-time.Minute), nil, int64(0), nil}}},
	)
	rec := f.do(http.MethodGet, "/api/queues", "", admin())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	decode(t, rec, &snap)
	if len(snap.Queues) != 1 {
		t.Fatalf("queues = %+v", snap.Queues)
	}
	q := snap.Queues[0]
	if q.Stage != "thumb_small" || q.Counts.Pending != 2 || q.WaitingReason == nil || *q.WaitingReason != WaitComponentOff ||
		q.Component == nil || *q.Component != "media.thumbnails" {
		t.Fatalf("queue summary = %+v, want thumb_small pending=2 waiting_reason=component_off component=media.thumbnails", q)
	}
}

// A component reason ranks under not_configured and over every live-count reason.
func TestWaitingReason_ComponentPrecedence(t *testing.T) {
	ptr := func(s string) *string { return &s }
	in := stageInputs{
		Info:      StageInfo{Stage: "transcribe", Enabled: false, DisabledReason: ptr("component_held")},
		PausedAll: true, PoolSize: 2, Now: t0,
	}
	if got := waitingReason(in); got == nil || *got != WaitComponentHeld {
		t.Fatalf("got %v, want component_held", got)
	}
}
