package updater

// Story 35.15 AC1: the supervisor's update metrics on its loopback admin's /metrics (the
// API's /metrics merges them in: one scrape per machine). What the supervisor owns is the
// database step its release's start runs: how long each update's migrations took. They are
// kept in the updates folder (metrics.json, the newest metricsKeep), not counted in memory,
// so a supervisor that restarts (an update restarts it) shows them again (EC1).

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// MetricMigrationDuration is how long an update's migrations took (design §16).
const MetricMigrationDuration = "maktaba_update_migration_duration_seconds"

// MetricsFile is the record in the updates folder.
const MetricsFile = "metrics.json"

// metricsKeep is how many migrations the record keeps.
const metricsKeep = 50

// migrationBuckets (seconds): a few seconds to an hour.
var migrationBuckets = []float64{1, 5, 15, 60, 300, 900, 1800, 3600}

// Migration is one update's migrations: the release they brought the database to and how
// long they took.
type Migration struct {
	At      time.Time `json:"at"`
	Version string    `json:"version"`
	Seconds float64   `json:"seconds"`
}

type metricsRecord struct {
	Migrations []Migration `json:"migrations"`
}

var metricsMu sync.Mutex

func readMetrics(dir string) metricsRecord {
	var rec metricsRecord
	if b, err := os.ReadFile(filepath.Join(dir, MetricsFile)); err == nil {
		_ = json.Unmarshal(b, &rec)
	}
	return rec
}

// RecordMigration adds an update's migrations to the record in the updates folder dir
// (best effort: "" or a failure to write records nothing).
func RecordMigration(dir, version string, d time.Duration, at time.Time) error {
	if dir == "" {
		return nil
	}
	metricsMu.Lock()
	defer metricsMu.Unlock()
	rec := readMetrics(dir)
	rec.Migrations = append(rec.Migrations, Migration{At: at.UTC(), Version: version, Seconds: d.Seconds()})
	if n := len(rec.Migrations); n > metricsKeep {
		rec.Migrations = rec.Migrations[n-metricsKeep:]
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return layout.WriteFileAtomic(filepath.Join(dir, MetricsFile), b, 0o644)
}

// WriteMetrics writes the supervisor's update metrics in Prometheus's text format, from
// the record in the updates folder dir.
func WriteMetrics(w io.Writer, dir string) error {
	metricsMu.Lock()
	rec := readMetrics(dir)
	metricsMu.Unlock()
	var sum float64
	counts := make([]uint64, len(migrationBuckets))
	for _, m := range rec.Migrations {
		sum += m.Seconds
		for i, b := range migrationBuckets {
			if m.Seconds <= b {
				counts[i]++
			}
		}
	}
	n := MetricMigrationDuration
	if _, err := fmt.Fprintf(w, "# HELP %s How long each update's database migrations took.\n# TYPE %s histogram\n", n, n); err != nil {
		return err
	}
	for i, b := range migrationBuckets {
		if _, err := fmt.Fprintf(w, "%s_bucket{le=\"%s\"} %d\n", n, strconv.FormatFloat(b, 'g', -1, 64), counts[i]); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n%s_sum %s\n%s_count %d\n", n, len(rec.Migrations), n,
		strconv.FormatFloat(sum, 'g', -1, 64), n, len(rec.Migrations))
	return err
}
