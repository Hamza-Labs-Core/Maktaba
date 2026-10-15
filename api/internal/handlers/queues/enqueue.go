package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Enqueue outcomes returned by processing_jobs_enqueue (plan-06-11 §4.2).
const (
	OutcomeInserted        = "inserted"
	OutcomeCoalesced       = "coalesced"
	OutcomeRerunRequested  = "rerun_requested"
	OutcomeDisabled        = "disabled"
	OutcomeUnknownStage    = "unknown_stage"
	OutcomeInvalidSubject  = "invalid_subject"
	enqueueFunctionSQLText = `SELECT job_id, outcome FROM processing_jobs_enqueue(
	$1::text, $2::uuid, $3::text, $4::int, $5::boolean, $6::boolean,
	$7::jsonb, $8::bigint, $9::jsonb, $10::int)`
)

// EnqueueSQL is the single statement through which Go enqueues or
// re-triggers work. Python calls the same SQL function, so the two
// languages cannot disagree on coalescing (plan-06-11 D8).
const EnqueueSQL = enqueueFunctionSQLText

// Querier is the slice of *sql.DB / *sql.Tx that Enqueue needs.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// EnqueueParams mirrors the SQL function's arguments. Nil/zero fields
// map to the function's defaults.
type EnqueueParams struct {
	SubjectKind  string
	SubjectID    string
	Stage        string
	Priority     *int
	Force        bool
	ForceCascade bool
	Reason       map[string]any
	TriggeredBy  *int64
	Payload      map[string]any
	MaxAttempts  *int
}

// EnqueueResult is one row returned by the SQL function.
type EnqueueResult struct {
	JobID   *int64
	Outcome string
}

// Accepted reports whether the outcome means a live row now exists.
func (r EnqueueResult) Accepted() bool {
	switch r.Outcome {
	case OutcomeInserted, OutcomeCoalesced, OutcomeRerunRequested:
		return true
	}
	return false
}

// ErrNoEnqueueRow means the SQL function returned no row at all, which
// only happens when the migration that defines it has not been applied.
var ErrNoEnqueueRow = errors.New("processing_jobs_enqueue returned no row")

// Enqueue calls processing_jobs_enqueue.
func Enqueue(ctx context.Context, q Querier, p EnqueueParams) (EnqueueResult, error) {
	reason, err := json.Marshal(nonNil(p.Reason))
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("marshal reason: %w", err)
	}
	var payload any
	if p.Payload != nil {
		b, err := json.Marshal(p.Payload)
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("marshal payload: %w", err)
		}
		payload = string(b)
	}
	var prio, maxAttempts, trig any
	if p.Priority != nil {
		prio = *p.Priority
	}
	if p.MaxAttempts != nil {
		maxAttempts = *p.MaxAttempts
	}
	if p.TriggeredBy != nil {
		trig = *p.TriggeredBy
	}
	var id sql.NullInt64
	var outcome string
	err = q.QueryRowContext(ctx, EnqueueSQL,
		p.SubjectKind, p.SubjectID, p.Stage, prio, p.Force, p.ForceCascade,
		string(reason), trig, payload, maxAttempts,
	).Scan(&id, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return EnqueueResult{}, ErrNoEnqueueRow
	}
	if err != nil {
		return EnqueueResult{}, err
	}
	res := EnqueueResult{Outcome: outcome}
	if id.Valid {
		v := id.Int64
		res.JobID = &v
	}
	return res, nil
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// ReasonFor builds the p_reason document: {"why", "by", ...extra}.
// "by" is "user:<id>" for an authenticated request, "system" otherwise.
func ReasonFor(ctx context.Context, why string, extra map[string]any) map[string]any {
	by := "system"
	if p := principal.FromContext(ctx); p != nil && p.UserID != "" {
		by = "user:" + p.UserID
	}
	out := map[string]any{"why": why, "by": by}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
