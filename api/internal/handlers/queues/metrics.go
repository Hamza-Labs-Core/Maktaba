package queues

// The queue's numbers on the home's /metrics, for the Queue and Processing dashboards (Epic 53
// story 53.12). The pipeline has no Prometheus endpoint (its workers are many processes on
// many machines), and every job is a processing_jobs row, so the API reads the table at a
// scrape, as the plugins' health metrics do:
//
//   - maktaba_pipeline_queue_depth{stage,state}     jobs waiting or running now (pending,
//     claimed, running, paused, resuming)
//   - maktaba_pipeline_jobs_last_hour{stage,outcome} jobs that ended in the last hour
//     (done, failed)
//   - maktaba_pipeline_audio_seconds_last_hour      seconds of audio transcribed in the last hour
//   - maktaba_pipeline_realtime_factor{stage}       the mean speed (media seconds per wall
//     second) of the jobs that ended in the last hour
//
// The answer is kept for a minute: the log index scrapes every 15 s, and the last hour's
// count reads the finished jobs. Only the home registers it: a joined machine reads the same
// table and would report it twice.

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

var (
	depthDesc = prometheus.NewDesc("maktaba_pipeline_queue_depth",
		"Jobs waiting or running, by stage and state (pending, claimed, running, paused, resuming) (Epic 53).",
		[]string{"stage", "state"}, nil)
	lastHourDesc = prometheus.NewDesc("maktaba_pipeline_jobs_last_hour",
		"Jobs that ended in the last hour, by stage and outcome (done, failed) (Epic 53).",
		[]string{"stage", "outcome"}, nil)
	audioDesc = prometheus.NewDesc("maktaba_pipeline_audio_seconds_last_hour",
		"Seconds of audio transcribed by the jobs that ended in the last hour (Epic 53).",
		nil, nil)
	speedDesc = prometheus.NewDesc("maktaba_pipeline_realtime_factor",
		"The mean real-time factor of the jobs that ended in the last hour, by stage (Epic 53).",
		[]string{"stage"}, nil)
)

// TranscribeStage is the stage whose audio seconds are counted.
const TranscribeStage = "transcribe"

// MetricsCacheFor is how long one reading of the table answers scrapes.
const MetricsCacheFor = time.Minute

const (
	depthSQL = `SELECT stage, state, count(*) FROM processing_jobs
	WHERE state IN ('pending', 'claimed', 'running', 'paused', 'resuming')
	GROUP BY stage, state`
	lastHourSQL = `SELECT stage, state, count(*),
	COALESCE(sum(total_duration_seconds) FILTER (WHERE state = 'done'), 0),
	avg(realtime_factor) FILTER (WHERE realtime_factor > 0)
	FROM processing_jobs
	WHERE finished_at > now() - interval '1 hour' AND state IN ('done', 'failed')
	GROUP BY stage, state`
)

// QueueMetrics is one reading of the table.
type QueueMetrics struct {
	Depth    []StageCount // by stage and state
	LastHour []StageCount // by stage and outcome
	// AudioSeconds is the transcribe stage's done jobs' media length.
	AudioSeconds float64
	// Speed is the mean real-time factor by stage (only stages that report one).
	Speed map[string]float64
}

// StageCount is a count of jobs with one stage and one state.
type StageCount struct {
	Stage, State string
	N            int64
}

// ReadQueueMetrics reads the table (empty when it cannot be read: a scrape never fails for it).
func ReadQueueMetrics(ctx context.Context, db *sql.DB) QueueMetrics {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m := QueueMetrics{Speed: map[string]float64{}}
	if rows, err := db.QueryContext(ctx, depthSQL); err == nil {
		for rows.Next() {
			var c StageCount
			if rows.Scan(&c.Stage, &c.State, &c.N) == nil {
				m.Depth = append(m.Depth, c)
			}
		}
		_ = rows.Close()
	}
	rows, err := db.QueryContext(ctx, lastHourSQL)
	if err != nil {
		return m
	}
	defer rows.Close()
	type speed struct{ sum, n float64 }
	speeds := map[string]*speed{}
	for rows.Next() {
		var c StageCount
		var audio float64
		var rtf sql.NullFloat64
		if rows.Scan(&c.Stage, &c.State, &c.N, &audio, &rtf) != nil {
			continue
		}
		m.LastHour = append(m.LastHour, c)
		if c.Stage == TranscribeStage {
			m.AudioSeconds += audio
		}
		if rtf.Valid && c.N > 0 {
			s := speeds[c.Stage]
			if s == nil {
				s = &speed{}
				speeds[c.Stage] = s
			}
			// the mean over both outcomes, weighted by their jobs
			s.sum += rtf.Float64 * float64(c.N)
			s.n += float64(c.N)
		}
	}
	for stage, s := range speeds {
		m.Speed[stage] = s.sum / s.n
	}
	return m
}

// QueueCollector exports a database's queue numbers, read at most once per MetricsCacheFor.
type QueueCollector struct {
	DB  *sql.DB
	Now func() time.Time

	mu   sync.Mutex
	at   time.Time
	last QueueMetrics
}

func (c *QueueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- depthDesc
	ch <- lastHourDesc
	ch <- audioDesc
	ch <- speedDesc
}

func (c *QueueCollector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	if c.DB == nil {
		c.mu.Unlock()
		return
	}
	if c.at.IsZero() || now().Sub(c.at) >= MetricsCacheFor {
		c.last, c.at = ReadQueueMetrics(context.Background(), c.DB), now()
	}
	m := c.last
	c.mu.Unlock()
	for _, d := range m.Depth {
		ch <- prometheus.MustNewConstMetric(depthDesc, prometheus.GaugeValue, float64(d.N), d.Stage, d.State)
	}
	for _, d := range m.LastHour {
		ch <- prometheus.MustNewConstMetric(lastHourDesc, prometheus.GaugeValue, float64(d.N), d.Stage, d.State)
	}
	ch <- prometheus.MustNewConstMetric(audioDesc, prometheus.GaugeValue, m.AudioSeconds)
	for stage, v := range m.Speed {
		ch <- prometheus.MustNewConstMetric(speedDesc, prometheus.GaugeValue, v, stage)
	}
}

var (
	queueMetrics     = &QueueCollector{}
	queueMetricsDB   atomic.Pointer[sql.DB]
	queueMetricsOnce sync.Once
)

// RegisterMetrics exports the queue's numbers from db on the process registry the API's
// /metrics serves (the home's; a later call replaces the database, the collector is
// registered once).
func RegisterMetrics(db *sql.DB) {
	if db == nil {
		return
	}
	queueMetricsDB.Store(db)
	queueMetricsOnce.Do(func() { _ = metrics.Reg().Register(registeredCollector{}) })
}

// registeredCollector reads the database RegisterMetrics last named.
type registeredCollector struct{}

func (registeredCollector) Describe(ch chan<- *prometheus.Desc) { queueMetrics.Describe(ch) }

func (registeredCollector) Collect(ch chan<- prometheus.Metric) {
	db := queueMetricsDB.Load()
	if db == nil {
		return
	}
	queueMetrics.mu.Lock()
	if queueMetrics.DB != db {
		queueMetrics.DB, queueMetrics.at = db, time.Time{}
	}
	queueMetrics.mu.Unlock()
	queueMetrics.Collect(ch)
}
