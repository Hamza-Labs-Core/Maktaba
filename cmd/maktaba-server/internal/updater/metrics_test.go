package updater

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TC1 (the supervisor's): the migrations' time is kept in the updates folder, so a
// supervisor that restarts shows it again (EC1), as a Prometheus histogram.
func TestMigrationMetricsKept(t *testing.T) {
	dir := t.TempDir()
	var empty bytes.Buffer
	if err := WriteMetrics(&empty, dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty.String(), MetricMigrationDuration+"_count 0") {
		t.Fatalf("no migrations yet:\n%s", empty.String())
	}
	at := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	for i, d := range []time.Duration{3 * time.Second, 40 * time.Second, 20 * time.Minute} {
		if err := RecordMigration(dir, "1.5."+string(rune('0'+i)), d, at); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := WriteMetrics(&out, dir); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"# TYPE " + MetricMigrationDuration + " histogram",
		MetricMigrationDuration + `_bucket{le="5"} 1`,
		MetricMigrationDuration + `_bucket{le="60"} 2`,
		MetricMigrationDuration + `_bucket{le="1800"} 3`,
		MetricMigrationDuration + `_bucket{le="+Inf"} 3`,
		MetricMigrationDuration + "_count 3",
		MetricMigrationDuration + "_sum 1243",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	for i := 0; i < metricsKeep+5; i++ {
		_ = RecordMigration(dir, "1.6.0", time.Second, at)
	}
	if n := len(readMetrics(dir).Migrations); n != metricsKeep {
		t.Fatalf("kept %d migrations", n)
	}
	if err := RecordMigration("", "1.6.0", time.Second, at); err != nil {
		t.Fatalf("no updates folder: %v", err)
	}
}
