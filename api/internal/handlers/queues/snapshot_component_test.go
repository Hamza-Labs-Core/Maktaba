package queues

import "testing"

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
