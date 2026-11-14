package broadcast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Story 50.28 §2: the cloud reads the edge's Analytics Engine events (dataset broadcast_events)
// through Cloudflare's SQL API. The columns are the schema's (testdata/events_schema.json): blob1
// is the event, blob2 the publisher, and so on. Every count is weighted by _sample_interval,
// because Analytics Engine samples at high volume.

// EventsDataset is the edge's Analytics Engine dataset.
const EventsDataset = "broadcast_events"

// Event columns, by the schema's names (events_schema.json; TestEventsSchema checks them).
var (
	blobColumns   = []string{"event", "publisher", "channel", "country", "device", "access", "mode", "rung", "code", "from", "to", "session"}
	doubleColumns = []string{"startup_ms", "minutes", "rebuffer_ms", "bitrate_kbps", "switch_ms", "segment_bytes", "upload_to_visible_ms", "glass_to_glass_ms"}
)

// col is the Analytics Engine column holding a schema name: blobN or doubleN.
func col(name string) string {
	for i, n := range blobColumns {
		if n == name {
			return "blob" + strconv.Itoa(i+1)
		}
	}
	for i, n := range doubleColumns {
		if n == name {
			return "double" + strconv.Itoa(i+1)
		}
	}
	panic("broadcast: no event column " + name)
}

// ErrAnalyticsUnavailable is the SQL API failing or not configured: the rollup tries again later.
var ErrAnalyticsUnavailable = errors.New("broadcast: Analytics Engine SQL API unavailable")

// Row is one result row, by column alias.
type Row map[string]any

// Str is a column as text.
func (r Row) Str(k string) string {
	switch v := r[k].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// Num is a column as a number. ClickHouse's JSON output quotes 64-bit integers, so a string
// holding a number counts too.
func (r Row) Num(k string) float64 {
	switch v := r[k].(type) {
	case float64:
		return v
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return f
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

// Querier runs one SQL query against Analytics Engine.
type Querier interface {
	Query(ctx context.Context, sql string) ([]Row, error)
}

// SQLClient is the Analytics Engine SQL API: POST <base>/accounts/<id>/analytics_engine/sql with
// the query as the body and an API token (Account Analytics: Read) as the bearer.
type SQLClient struct {
	BaseURL   string // https://api.cloudflare.com/client/v4 (a fake in tests)
	AccountID string
	Token     string
	HTTP      *http.Client
}

// Query implements Querier.
func (c *SQLClient) Query(ctx context.Context, sql string) ([]Row, error) {
	if c == nil || c.AccountID == "" || c.Token == "" {
		return nil, ErrAnalyticsUnavailable
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/accounts/"+c.AccountID+"/analytics_engine/sql", strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "text/plain")
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnalyticsUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnalyticsUnavailable, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d: %.200s", ErrAnalyticsUnavailable, res.StatusCode, body)
	}
	var out struct {
		Data []Row `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: bad answer: %v", ErrAnalyticsUnavailable, err)
	}
	return out.Data, nil
}

// aeTime is a time as Analytics Engine's SQL reads it.
func aeTime(t time.Time) string {
	return "toDateTime('" + t.UTC().Format("2006-01-02 15:04:05") + "')"
}

// hourQuery sums an hour's events by event and dimension (one row per event, publisher, channel,
// country, device, rung and code), every number weighted by _sample_interval.
func hourQuery(from, to time.Time) string {
	return fmt.Sprintf(`SELECT %s AS event, %s AS publisher, %s AS channel, %s AS country, %s AS device, %s AS rung, %s AS code,
  SUM(_sample_interval) AS n,
  SUM(_sample_interval * %s) AS startup_ms,
  SUM(_sample_interval * %s) AS minutes,
  SUM(_sample_interval * %s) AS rebuffer_ms,
  SUM(_sample_interval * %s) AS segment_bytes
FROM %s
WHERE timestamp >= %s AND timestamp < %s AND %s IN ('play', 'beat', 'error', 'blocked', 'ingest')
GROUP BY event, publisher, channel, country, device, rung, code
FORMAT JSON`,
		col("event"), col("publisher"), col("channel"), col("country"), col("device"), col("rung"), col("code"),
		col("startup_ms"), col("minutes"), col("rebuffer_ms"), col("segment_bytes"),
		EventsDataset, aeTime(from), aeTime(to), col("event"))
}

// peakQuery counts each minute's beats per channel: a session beats once a minute, so a
// minute's weighted beats are its concurrent viewers.
func peakQuery(from, to time.Time) string {
	return fmt.Sprintf(`SELECT %s AS publisher, %s AS channel, toStartOfInterval(timestamp, INTERVAL '1' MINUTE) AS minute,
  SUM(_sample_interval) AS n
FROM %s
WHERE timestamp >= %s AND timestamp < %s AND %s = 'beat'
GROUP BY publisher, channel, minute
FORMAT JSON`,
		col("publisher"), col("channel"), EventsDataset, aeTime(from), aeTime(to), col("event"))
}

// LagBuckets are maktaba_broadcast_ingest_lag_seconds' buckets (upload to visible).
var LagBuckets = []float64{0.25, 0.5, 1, 2, 3, 5, 10, 30}

// minuteQuery is the monitor's minute: beats per channel (viewers now) and ingest per channel
// (channels live), with the ingest lag's buckets.
func minuteQuery(from, to time.Time) string {
	var b strings.Builder
	for i, le := range LagBuckets {
		fmt.Fprintf(&b, "  SUM(IF(%s <= %g, _sample_interval, 0)) AS le_%d,\n", col("upload_to_visible_ms"), le*1000, i)
	}
	return fmt.Sprintf(`SELECT %s AS event, %s AS publisher, %s AS channel,
  SUM(_sample_interval) AS n,
%s  SUM(_sample_interval * %s) AS lag_ms
FROM %s
WHERE timestamp >= %s AND timestamp < %s AND %s IN ('beat', 'ingest')
GROUP BY event, publisher, channel
FORMAT JSON`,
		col("event"), col("publisher"), col("channel"), b.String(), col("upload_to_visible_ms"),
		EventsDataset, aeTime(from), aeTime(to), col("event"))
}
