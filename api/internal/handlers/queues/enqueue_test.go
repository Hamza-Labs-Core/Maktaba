package queues

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

func TestEnqueue_ArgsAndOutcomes(t *testing.T) {
	prio, ma := 50, 5
	trig := int64(77)
	tests := []struct {
		name     string
		p        EnqueueParams
		row      []driver.Value
		wantArgs []any // positions 0..9; "*" = any non-nil
		wantRes  EnqueueResult
		accepted bool
	}{
		{
			name: "defaults map to NULL", p: EnqueueParams{SubjectKind: "video", SubjectID: uidA, Stage: "probe"},
			row:      []driver.Value{int64(1), "inserted"},
			wantArgs: []any{"video", uidA, "probe", nil, false, false, "{}", nil, nil, nil},
			wantRes:  EnqueueResult{JobID: i64(1), Outcome: OutcomeInserted}, accepted: true,
		},
		{
			name: "all fields", p: EnqueueParams{SubjectKind: "season", SubjectID: uidB, Stage: "tag", Priority: &prio, Force: true,
				ForceCascade: true, Reason: map[string]any{"why": "x"}, TriggeredBy: &trig, Payload: map[string]any{"seasons": []string{"a"}}, MaxAttempts: &ma},
			row:      []driver.Value{int64(2), "coalesced"},
			wantArgs: []any{"season", uidB, "tag", int64(50), true, true, `{"why":"x"}`, int64(77), `{"seasons":["a"]}`, int64(5)},
			wantRes:  EnqueueResult{JobID: i64(2), Outcome: OutcomeCoalesced}, accepted: true,
		},
		{
			name: "rerun requested is accepted", p: EnqueueParams{SubjectKind: "library", SubjectID: uidA, Stage: "detect"},
			row: []driver.Value{int64(3), "rerun_requested"}, wantRes: EnqueueResult{JobID: i64(3), Outcome: OutcomeRerunRequested}, accepted: true,
		},
		{
			name: "disabled has no job", p: EnqueueParams{SubjectKind: "library", SubjectID: uidA, Stage: "plex_collection"},
			row: []driver.Value{nil, "disabled"}, wantRes: EnqueueResult{Outcome: OutcomeDisabled},
		},
		{
			name: "invalid subject", p: EnqueueParams{SubjectKind: "video", SubjectID: uidA, Stage: "detect"},
			row: []driver.Value{nil, "invalid_subject"}, wantRes: EnqueueResult{Outcome: OutcomeInvalidSubject},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := queuestest.New(&queuestest.Rule{Match: "from processing_jobs_enqueue(", Cols: []string{"job_id", "outcome"}, Rows: [][]driver.Value{tc.row}})
			got, err := Enqueue(context.Background(), db.DB, tc.p)
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != tc.wantRes.Outcome || (got.JobID == nil) != (tc.wantRes.JobID == nil) ||
				(got.JobID != nil && *got.JobID != *tc.wantRes.JobID) {
				t.Errorf("result = %+v want %+v", got, tc.wantRes)
			}
			if got.Accepted() != tc.accepted {
				t.Errorf("Accepted = %v", got.Accepted())
			}
			args := db.Calls()[0].Args
			if len(args) != 10 {
				t.Fatalf("function takes 10 arguments, got %d", len(args))
			}
			for i, w := range tc.wantArgs {
				if args[i] != w {
					t.Errorf("arg %d = %#v want %#v", i, args[i], w)
				}
			}
		})
	}
}

func i64(v int64) *int64 { return &v }

func TestEnqueue_SQLShape(t *testing.T) {
	n := queuestest.Norm(EnqueueSQL)
	for _, want := range []string{"select job_id, outcome from processing_jobs_enqueue(", "$10::int"} {
		if !strings.Contains(n, want) {
			t.Errorf("EnqueueSQL missing %q", want)
		}
	}
}

func TestEnqueue_Errors(t *testing.T) {
	t.Run("no row", func(t *testing.T) {
		db := queuestest.New()
		if _, err := Enqueue(context.Background(), db.DB, EnqueueParams{}); !errors.Is(err, ErrNoEnqueueRow) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("db error", func(t *testing.T) {
		db := queuestest.New(&queuestest.Rule{Match: "processing_jobs_enqueue", Err: errors.New("function does not exist")})
		if _, err := Enqueue(context.Background(), db.DB, EnqueueParams{}); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestReasonFor(t *testing.T) {
	ctx := principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "u9"})
	r := ReasonFor(ctx, "retry", map[string]any{"of_job": 4})
	if r["why"] != "retry" || r["by"] != "user:u9" || r["of_job"] != 4 {
		t.Errorf("reason = %v", r)
	}
	if r := ReasonFor(context.Background(), "x", nil); r["by"] != "system" {
		t.Errorf("anonymous reason = %v", r)
	}
}
