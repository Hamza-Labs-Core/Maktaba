package privacy

import (
	"context"
	"database/sql"
	"time"
)

// Retention runs the GDPR storage-limitation purges over the relay
// metrics tables. The metrics Runner calls PurgeHourly on its daily tick
// (the Runner already purges raw at 24h via its own hourly tick).
type Retention struct {
	DB *sql.DB
}

// NewRetention wires the purge to a pool.
func NewRetention(db *sql.DB) *Retention { return &Retention{DB: db} }

// PurgeHourly deletes hourly rollup rows older than RetentionDays (90).
// Signature matches metrics.HourlyPurger so it can be wired directly into
// the Runner without an import cycle.
func (r *Retention) PurgeHourly(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.UTC().AddDate(0, 0, -RetentionDays)
	res, err := r.DB.ExecContext(ctx,
		`DELETE FROM relay_metrics_hourly WHERE hour < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DataSubjectService executes a right-to-erasure (Art. 17) for a deleted
// account across the relay's user-linked data.
type DataSubjectService struct {
	DB *sql.DB
}

// NewDataSubjectService wires the service to a pool.
func NewDataSubjectService(db *sql.DB) *DataSubjectService {
	return &DataSubjectService{DB: db}
}

// DeletionReport summarises what an erasure touched.
type DeletionReport struct {
	UserID      string `json:"user_id"`
	PushRows    int64  `json:"push_log_rows_deleted"`
	MetricsRows int64  `json:"metrics_rows_deleted"`
	Note        string `json:"note"`
}

// Delete erases the account's personal data from the relay. Only
// push_dispatch_log is keyed to a user; the aggregate metrics tables hold
// no user data by design (README D1), so they require — and receive — no
// change. The report states that explicitly so an auditor can see the
// erasure was complete, not merely silent.
func (s *DataSubjectService) Delete(ctx context.Context, userID string) (DeletionReport, error) {
	rep := DeletionReport{
		UserID: userID,
		Note:   "Relay analytics are aggregate-only (no user/server id, no IP); no analytics rows reference this user.",
	}
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM push_dispatch_log WHERE user_id = $1`, userID)
	if err != nil {
		return rep, err
	}
	rep.PushRows, _ = res.RowsAffected()
	return rep, nil
}
