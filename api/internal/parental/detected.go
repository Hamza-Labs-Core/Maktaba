package parental

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// DetectLineKey is the app_settings key of the confidence a detected tier needs to
// count (story 57.17): a number from 0 to 1.
const DetectLineKey = "parental.detect_min_confidence"

// DefaultDetectLine is the line of a home that never set one.
const DefaultDetectLine = 0.8

// Querier is what DetectLine reads through: a *sql.DB or a *sql.Tx.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DetectLine is the confidence at or above which a detected tier rates a video. An
// absent key, and a value that is not a number from 0 to 1, give DefaultDetectLine:
// a broken setting never lowers the line. An error reading it is returned, so the
// caller refuses instead of guessing.
func DetectLine(ctx context.Context, q Querier) (float64, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, DetectLineKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultDetectLine, nil
	}
	if err != nil {
		return 0, fmt.Errorf("parental: read %s: %w", DetectLineKey, err)
	}
	return parseDetectLine(raw), nil
}

func parseDetectLine(raw []byte) float64 {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return DefaultDetectLine
	}
	f, ok := v.(float64)
	if !ok || f < 0 || f > 1 {
		return DefaultDetectLine
	}
	return f
}
