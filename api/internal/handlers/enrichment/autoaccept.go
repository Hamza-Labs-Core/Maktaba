package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
)

// AutoAcceptSettingsKey is the rules section of the review queue (story
// 26.6): app_settings key rules.enrichment holds only the values that differ
// from the defaults (pipeline/src/maktaba_pipeline/enrichment_settings.py).
const AutoAcceptSettingsKey = "rules.enrichment"

// DefaultAutoAcceptInterval is how often RunAutoAccept sweeps the queue.
const DefaultAutoAcceptInterval = time.Minute

// LoadAutoAcceptThreshold reads rules.enrichment.auto_accept_threshold. No
// row, no value, or a value outside (0, 1] is 0: off (the default).
func LoadAutoAcceptThreshold(ctx context.Context, db *sql.DB) (float64, error) {
	if db == nil {
		return 0, nil
	}
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, AutoAcceptSettingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var stored struct {
		Threshold *float64 `json:"auto_accept_threshold"`
	}
	if json.Unmarshal(raw, &stored) != nil || stored.Threshold == nil {
		return 0, nil
	}
	t := *stored.Threshold
	if t <= 0 || t > 1 {
		return 0, nil
	}
	return t, nil
}

// AutoAcceptResult is what one sweep did.
type AutoAcceptResult struct {
	Threshold float64        `json:"threshold"`
	Accepted  []AcceptResult `json:"accepted"`
}

// AutoAcceptOnce accepts, for every video with a waiting match, its best
// candidate when that candidate's confidence is at least the configured
// threshold. A video that already has an accepted candidate is left alone.
// Each accept goes through the same promotion as the Accept button: fields
// the user edited are skipped, every applied field keeps its previous value
// for Revert, and the decision is recorded as "auto_accept". Threshold 0
// (the default) does nothing.
func (h *Handler) AutoAcceptOnce(ctx context.Context) (AutoAcceptResult, error) {
	out := AutoAcceptResult{Accepted: []AcceptResult{}}
	if h.DB == nil {
		return out, nil
	}
	threshold, err := LoadAutoAcceptThreshold(ctx, h.DB)
	if err != nil {
		return out, err
	}
	out.Threshold = threshold
	if threshold == 0 {
		return out, nil
	}
	rows, err := h.DB.QueryContext(ctx, `
		SELECT e.video_id, e.external_id, e.confidence
		FROM media_metadata_enrichment e
		WHERE e.is_dismissed = false AND e.is_accepted = false
		  AND NOT EXISTS (SELECT 1 FROM media_metadata_enrichment a
		                  WHERE a.video_id = e.video_id AND a.is_accepted = true)
		ORDER BY e.video_id, e.confidence DESC
	`)
	if err != nil {
		return out, err
	}
	type pick struct{ video, ext string }
	var picks []pick
	seen := map[string]bool{}
	for rows.Next() {
		var vid, ext string
		var conf float64
		if rows.Scan(&vid, &ext, &conf) != nil {
			continue
		}
		if seen[vid] {
			continue // only the best candidate per video decides
		}
		seen[vid] = true
		if conf >= threshold {
			picks = append(picks, pick{vid, ext})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	_ = rows.Close()
	for _, p := range picks {
		res, e := h.applyAccept(ctx, nil, p.video, p.ext, "", "auto_accept")
		if e != nil {
			continue // a video removed or changed mid-sweep waits for the next one
		}
		out.Accepted = append(out.Accepted, res)
	}
	return out, nil
}

// RunAutoAccept sweeps the review queue every interval (0: the default)
// until ctx ends. Errors are logged and the next tick tries again.
func (h *Handler) RunAutoAccept(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = DefaultAutoAcceptInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		res, err := h.AutoAcceptOnce(ctx)
		if err != nil {
			logger.Warn("enrichment auto-accept sweep failed", "event", "enrich_auto_accept_error", "err", err)
		} else if len(res.Accepted) > 0 {
			logger.Info("enrichment auto-accepted matches", "event", "enrich_auto_accept",
				"count", len(res.Accepted), "threshold", res.Threshold)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
