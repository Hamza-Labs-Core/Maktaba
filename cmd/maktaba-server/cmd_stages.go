package main

// `maktaba-server stages impl <stage> <inproc|plugin>` (Epic 54, story 54.25 §3): where one
// of Maktaba's own stages runs, the same flip as PUT /api/queues/{stage}/impl, on the home's
// database from the home itself. The rollback of a wave, per stage, in seconds:
//
//	maktaba-server stages impl display_title inproc
//
// It stops new claims of the stage, waits for its running jobs (then pauses what did not
// end, with its checkpoint: no attempt spent), sets the stage's impl and resumes claims.
// The flip is audited (actor_source cli).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

func newStagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stages",
		Short: "Maktaba's own stages: where each runs (impl)",
	}
	var grace time.Duration
	impl := &cobra.Command{
		Use:   "impl <stage> <inproc|plugin>",
		Short: "Run a built-in stage in the pipeline worker (inproc) or in its plugin runner (plugin)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			_, dsn, ok := componentsTarget(cfg)
			if !ok {
				return fmt.Errorf("stages impl needs this home's Postgres database (a SQLite home runs every stage in-process)")
			}
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				return err
			}
			defer db.Close()
			return runStagesImpl(cmd.Context(), cmd.OutOrStdout(), db, args[0], args[1], grace)
		},
	}
	impl.Flags().DurationVar(&grace, "grace", 60*time.Second, "how long to wait for the stage's running jobs")
	cmd.AddCommand(impl)
	return cmd
}

// firstPartyStage reports whether a stage is one of the built-in plugins' (the bundle this
// binary embeds).
func firstPartyStage(stage string) bool {
	var doc struct {
		Plugins []struct {
			Statements []any  `json:"statements"`
			Manifest   string `json:"manifest"`
		} `json:"plugins"`
	}
	if json.Unmarshal(sharedplugins.FirstParty, &doc) != nil {
		return false
	}
	for _, p := range doc.Plugins {
		for _, s := range stageNamesOf(p.Manifest) {
			if s == stage {
				return true
			}
		}
	}
	return false
}

func runStagesImpl(ctx context.Context, out io.Writer, db *sql.DB, stage, impl string, grace time.Duration) error {
	if impl != "inproc" && impl != "plugin" {
		return fmt.Errorf("impl is inproc or plugin, not %q", impl)
	}
	if !firstPartyStage(stage) {
		return fmt.Errorf("%s is not a built-in plugin's stage", stage)
	}
	var cur string
	if err := db.QueryRowContext(ctx, `SELECT impl FROM job_stages WHERE stage = $1`, stage).Scan(&cur); err != nil {
		return fmt.Errorf("no queue %s: %w", stage, err)
	}
	if cur == impl {
		fmt.Fprintf(out, "%s already runs %s\n", stage, impl)
		return nil
	}
	if _, err := db.ExecContext(ctx, `UPDATE job_stages SET flipping = TRUE WHERE stage = $1`, stage); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_, _ = db.ExecContext(context.WithoutCancel(ctx), `UPDATE job_stages SET flipping = FALSE WHERE stage = $1`, stage)
		}
	}()
	end := time.Now().Add(grace)
	time.Sleep(200 * time.Millisecond)
	for {
		var running int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs WHERE stage = $1
			AND state IN ('claimed', 'running', 'resuming')`, stage).Scan(&running); err != nil {
			return err
		}
		if running == 0 || time.Now().After(end) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET state = 'paused', paused_reason = 'impl_flip',
		paused_at = now(), claimed_by = NULL WHERE stage = $1 AND state IN ('claimed', 'running', 'resuming')`, stage)
	if err != nil {
		return err
	}
	paused, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE job_stages SET impl = $2, flipping = FALSE, impl_changed_at = now(),
		impl_changed_by = 'cli' WHERE stage = $1`, stage, impl); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"from": cur, "to": impl, "paused": paused})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (category, action, target_id, target_kind, actor_source, payload)
		VALUES ('components', 'stage.impl', $1, 'stage', 'cli', $2)`, stage, string(payload)); err != nil {
		return err
	}
	_, _ = tx.ExecContext(ctx, `SELECT pg_notify('job_stages_changed', $1)`, stage)
	if err := tx.Commit(); err != nil {
		return err
	}
	done = true
	fmt.Fprintf(out, "%s now runs %s (%d running jobs paused, to resume with no attempt spent)\n", stage, impl, paused)
	return nil
}

// stageNamesOf reads a manifest's [[stage]] names.
func stageNamesOf(manifest string) []string {
	var m struct {
		Stage []struct {
			Name string `toml:"name"`
		} `toml:"stage"`
	}
	if _, err := toml.Decode(manifest, &m); err != nil {
		return nil
	}
	out := make([]string, 0, len(m.Stage))
	for _, s := range m.Stage {
		out = append(out, s.Name)
	}
	return out
}
