package metrics

import (
	"testing"
	"time"
)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func findRow(rows []RawRow, metric, country string) (RawRow, bool) {
	for _, r := range rows {
		if r.Metric == metric && r.Country == country {
			return r, true
		}
	}
	return RawRow{}, false
}

func TestCollectorAccumulatesAndResets(t *testing.T) {
	c := NewCollector()
	at := time.Date(2026, 6, 19, 10, 30, 45, 0, time.UTC)
	c.now = fixedNow(at)

	c.RecordBandwidth(100, 200)
	c.RecordBandwidth(50, 0)
	c.RecordRequest("DE")
	c.RecordRequest("DE")
	c.RecordRequest("US")
	c.RecordRequest("") // unknown country
	c.RecordPush(true)
	c.RecordPush(false)
	c.ObserveConnections(3, 3)

	rows := c.Snapshot()

	// Bucket is truncated to the minute.
	wantBucket := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	for _, r := range rows {
		if !r.Bucket.Equal(wantBucket) {
			t.Fatalf("bucket = %v, want %v", r.Bucket, wantBucket)
		}
	}

	if r, ok := findRow(rows, MetricBandwidthIn, ""); !ok || r.Value != 150 {
		t.Errorf("bandwidth_in = %+v, want value 150", r)
	}
	if r, ok := findRow(rows, MetricBandwidthOut, ""); !ok || r.Value != 200 {
		t.Errorf("bandwidth_out = %+v, want value 200", r)
	}
	if r, ok := findRow(rows, MetricRequests, "DE"); !ok || r.Value != 2 {
		t.Errorf("requests DE = %+v, want value 2", r)
	}
	if r, ok := findRow(rows, MetricRequests, "US"); !ok || r.Value != 1 {
		t.Errorf("requests US = %+v, want value 1", r)
	}
	if r, ok := findRow(rows, MetricRequests, ""); !ok || r.Value != 1 {
		t.Errorf("requests unknown = %+v, want value 1", r)
	}
	if r, ok := findRow(rows, MetricConnectedServers, ""); !ok || r.Value != 3 || r.Samples != 1 {
		t.Errorf("connected_servers = %+v, want value 3 samples 1", r)
	}

	// Snapshot resets the delta accumulator.
	if again := c.Snapshot(); again != nil {
		t.Errorf("second snapshot = %v, want nil after reset", again)
	}
}

func TestCollectorPromSnapshotIsCumulative(t *testing.T) {
	c := NewCollector()
	c.RecordBandwidth(1000, 2000)
	c.RecordRequest("DE")
	c.RecordPush(true)
	c.RecordPush(false)
	c.RecordPush(false)
	c.ObserveConnections(5, 4)

	// A flush must NOT reset the Prometheus cumulative view.
	_ = c.Snapshot()
	c.RecordBandwidth(500, 0)

	s := c.PromSnapshot()
	if s.BandwidthIn != 1500 {
		t.Errorf("BandwidthIn = %d, want 1500", s.BandwidthIn)
	}
	if s.BandwidthOut != 2000 {
		t.Errorf("BandwidthOut = %d, want 2000", s.BandwidthOut)
	}
	if s.Requests != 1 {
		t.Errorf("Requests = %d, want 1", s.Requests)
	}
	if s.PushSent != 1 || s.PushFailed != 2 {
		t.Errorf("push sent/failed = %d/%d, want 1/2", s.PushSent, s.PushFailed)
	}
	if s.ConnectedServers != 5 || s.ActiveTunnels != 4 {
		t.Errorf("gauges = %d/%d, want 5/4", s.ConnectedServers, s.ActiveTunnels)
	}
}

func TestCollectorLive(t *testing.T) {
	c := NewCollector()
	c.ObserveConnections(7, 6)
	if s, tn := c.Live(); s != 7 || tn != 6 {
		t.Errorf("Live = %d/%d, want 7/6", s, tn)
	}
}

func TestKindOf(t *testing.T) {
	gauges := []string{MetricConnectedServers, MetricActiveTunnels}
	for _, m := range gauges {
		if KindOf(m) != KindGauge {
			t.Errorf("KindOf(%q) = counter, want gauge", m)
		}
	}
	counters := []string{MetricBandwidthIn, MetricBandwidthOut, MetricRequests, MetricPushSent, MetricPushFailed, "unknown"}
	for _, m := range counters {
		if KindOf(m) != KindCounter {
			t.Errorf("KindOf(%q) = gauge, want counter", m)
		}
	}
}
