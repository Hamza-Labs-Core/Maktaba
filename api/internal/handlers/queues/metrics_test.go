package queues

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// Epic 53 story 53.12: the Queue and Processing dashboards' series come from processing_jobs
// at a scrape: the jobs waiting or running by stage and state, the last hour's ended jobs by
// outcome, the audio transcribed and the mean speed (weighted by the jobs of each outcome).
// One reading answers scrapes for a minute.
func TestQueueCollector(t *testing.T) {
	db := queuestest.New(
		&queuestest.Rule{Match: "where state in ('pending'", Cols: []string{"stage", "state", "count"},
			Rows: [][]driver.Value{{"transcribe", "pending", int64(12)}, {"transcribe", "running", int64(1)}, {"thumbnail", "pending", int64(3)}}},
		&queuestest.Rule{Match: "finished_at > now() - interval '1 hour'", Cols: []string{"stage", "state", "count", "audio", "rtf"},
			Rows: [][]driver.Value{
				{"transcribe", "done", int64(3), float64(5400), float64(2.0)},
				{"transcribe", "failed", int64(1), float64(0), float64(6.0)},
				{"thumbnail", "done", int64(40), float64(99), nil},
			}},
	)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c := &QueueCollector{DB: db.DB, Now: func() time.Time { return now }}
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	want := `
# HELP maktaba_pipeline_audio_seconds_last_hour Seconds of audio transcribed by the jobs that ended in the last hour (Epic 53).
# TYPE maktaba_pipeline_audio_seconds_last_hour gauge
maktaba_pipeline_audio_seconds_last_hour 5400
# HELP maktaba_pipeline_jobs_last_hour Jobs that ended in the last hour, by stage and outcome (done, failed) (Epic 53).
# TYPE maktaba_pipeline_jobs_last_hour gauge
maktaba_pipeline_jobs_last_hour{outcome="done",stage="thumbnail"} 40
maktaba_pipeline_jobs_last_hour{outcome="done",stage="transcribe"} 3
maktaba_pipeline_jobs_last_hour{outcome="failed",stage="transcribe"} 1
# HELP maktaba_pipeline_queue_depth Jobs waiting or running, by stage and state (pending, claimed, running, paused, resuming) (Epic 53).
# TYPE maktaba_pipeline_queue_depth gauge
maktaba_pipeline_queue_depth{stage="thumbnail",state="pending"} 3
maktaba_pipeline_queue_depth{stage="transcribe",state="pending"} 12
maktaba_pipeline_queue_depth{stage="transcribe",state="running"} 1
# HELP maktaba_pipeline_realtime_factor The mean real-time factor of the jobs that ended in the last hour, by stage (Epic 53).
# TYPE maktaba_pipeline_realtime_factor gauge
maktaba_pipeline_realtime_factor{stage="transcribe"} 3
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
	reads := func() int { return len(db.Find("from processing_jobs where state in")) }
	_ = testutil.CollectAndCount(c)
	if reads() != 1 {
		t.Fatalf("a scrape within the minute read the table again: %d reads", reads())
	}
	now = now.Add(MetricsCacheFor)
	_ = testutil.CollectAndCount(c)
	if reads() != 2 {
		t.Fatalf("a scrape after the minute did not read the table: %d reads", reads())
	}
}

// A table that cannot be read gives empty series (an audio total of 0), never a failed scrape.
func TestQueueCollectorWithoutTheTable(t *testing.T) {
	c := &QueueCollector{DB: queuestest.New().DB}
	if n := testutil.CollectAndCount(c); n != 1 {
		t.Fatalf("%d series, want the audio total alone", n)
	}
	if n := testutil.CollectAndCount(&QueueCollector{}); n != 0 {
		t.Fatalf("no database: %d series", n)
	}
}
