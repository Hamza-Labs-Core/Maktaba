package updates

// Story 35.15 AC3: every minute the home writes the update numbers the control centre's
// health view charts into health_samples (Epic 22.9 / 33.15):
//
//	update.state            0 up to date, 1 a release waits, 2 a run is under way,
//	                        3 the last run ended badly (failed, rolled back, held)
//	update.machines_behind  joined machines on an older release than the cluster's

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// Health sample metric names and the update.state values.
const (
	SampleUpdateState    = "update.state"
	SampleMachinesBehind = "update.machines_behind"

	UpdateStateUpToDate = 0
	UpdateStateBehind   = 1
	UpdateStateUpdating = 2
	UpdateStateTrouble  = 3

	updateHealthEvery = time.Minute
	// troubleFor: a run that ended badly is shown that long, unless a newer one ran
	troubleFor = 24 * time.Hour
)

// troubled are the run states that ended badly (a run whose only machines left need a
// person, done_needs_person, did not: those machines show as behind).
var troubled = map[string]bool{RunFailed: true, RunRolledBack: true, RunFailedRollback: true, RunHeld: true,
	RunDoneFailures: true}

// HealthSampler writes the update samples. Metrics gives machines_behind (the same read as
// /metrics); Behind reports whether a release waits for this home (nil: never).
type HealthSampler struct {
	Metrics *Metrics
	Behind  func(ctx context.Context) bool
	Logger  *slog.Logger
	// Host is the health_samples host column (default: the hostname).
	Host string
}

// Run writes the samples every minute until ctx ends.
func (h *HealthSampler) Run(ctx context.Context) {
	t := time.NewTicker(updateHealthEvery)
	defer t.Stop()
	for {
		if err := h.Sample(ctx); err != nil && ctx.Err() == nil && h.Logger != nil {
			h.Logger.Warn("update: health samples not written", "err", err, "event", "update_health_sample_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// State is the update.state value now.
func (h *HealthSampler) State(ctx context.Context) (int, error) {
	st := h.Metrics.Driver.Store
	run, err := st.ActiveRun(ctx)
	if err != nil {
		return 0, err
	}
	if run != nil {
		return UpdateStateUpdating, nil
	}
	last, err := st.Runs(ctx, 1)
	if err != nil {
		return 0, err
	}
	now := h.Metrics.Driver.now()
	if len(last) > 0 && troubled[last[0].State] && last[0].FinishedAt != nil && now.Sub(*last[0].FinishedAt) < troubleFor {
		return UpdateStateTrouble, nil
	}
	if h.Behind != nil && h.Behind(ctx) {
		return UpdateStateBehind, nil
	}
	return UpdateStateUpToDate, nil
}

// Sample writes one minute's samples.
func (h *HealthSampler) Sample(ctx context.Context) error {
	state, err := h.State(ctx)
	if err != nil {
		return err
	}
	st := h.Metrics.Driver.Store
	rows := []struct {
		metric string
		value  float64
	}{{SampleUpdateState, float64(state)}}
	if n, ok := h.Metrics.machinesBehind(ctx); ok {
		rows = append(rows, struct {
			metric string
			value  float64
		}{SampleMachinesBehind, float64(n)})
	}
	host := h.Host
	if host == "" {
		host, _ = os.Hostname()
	}
	at := st.ts(h.Metrics.Driver.now().Truncate(time.Minute))
	for _, r := range rows {
		if _, err := st.DB.ExecContext(ctx, `INSERT INTO health_samples (at, host, metric, value) VALUES ($1, $2, $3, $4)
			ON CONFLICT (metric, host, at) DO UPDATE SET value = excluded.value`, at, host, r.metric, r.value); err != nil {
			return err
		}
	}
	return nil
}
