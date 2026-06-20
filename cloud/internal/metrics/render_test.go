package metrics

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderPrometheus(t *testing.T) {
	var b bytes.Buffer
	s := PromSnapshot{
		ConnectedServers: 3,
		ActiveTunnels:    2,
		BandwidthIn:      1000,
		BandwidthOut:     2500,
		Requests:         42,
		PushSent:         10,
		PushFailed:       1,
	}
	if err := RenderPrometheus(&b, s); err != nil {
		t.Fatalf("RenderPrometheus: %v", err)
	}
	out := b.String()

	wantLines := []string{
		"# TYPE maktaba_relay_connected_servers gauge",
		"maktaba_relay_connected_servers 3",
		"# TYPE maktaba_relay_bandwidth_in_bytes_total counter",
		"maktaba_relay_bandwidth_in_bytes_total 1000",
		"maktaba_relay_requests_total 42",
		"maktaba_relay_push_failed_total 1",
	}
	for _, w := range wantLines {
		if !strings.Contains(out, w) {
			t.Errorf("prometheus output missing %q\n---\n%s", w, out)
		}
	}
	// Every series must carry a HELP and a TYPE line.
	if strings.Count(out, "# HELP ") != len(promSeriesList) {
		t.Errorf("HELP count = %d, want %d", strings.Count(out, "# HELP "), len(promSeriesList))
	}
	if strings.Count(out, "# TYPE ") != len(promSeriesList) {
		t.Errorf("TYPE count = %d, want %d", strings.Count(out, "# TYPE "), len(promSeriesList))
	}
}

func TestRenderCSV(t *testing.T) {
	var b bytes.Buffer
	rows := []ExportRow{
		{Hour: "2026-06-19T10:00:00Z", Metric: "requests", Country: "DE", SumValue: 5, MaxValue: 3, Samples: 0},
		{Hour: "2026-06-19T11:00:00Z", Metric: "connected_servers", Country: "", SumValue: 30, MaxValue: 4, Samples: 10},
	}
	if err := RenderCSV(&b, rows); err != nil {
		t.Fatalf("RenderCSV: %v", err)
	}
	recs, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(recs) != 3 { // header + 2 rows
		t.Fatalf("rows = %d, want 3", len(recs))
	}
	wantHeader := []string{"hour", "metric", "country", "sum_value", "max_value", "samples"}
	for i, h := range wantHeader {
		if recs[0][i] != h {
			t.Errorf("header[%d] = %q, want %q", i, recs[0][i], h)
		}
	}
	if recs[1][3] != "5" || recs[2][3] != "30" {
		t.Errorf("sum_value column = %q,%q, want 5,30", recs[1][3], recs[2][3])
	}
}

func TestRenderCSVEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := RenderCSV(&b, nil); err != nil {
		t.Fatalf("RenderCSV(nil): %v", err)
	}
	// Header only.
	if got := strings.TrimSpace(b.String()); got != "hour,metric,country,sum_value,max_value,samples" {
		t.Errorf("empty CSV = %q", got)
	}
}

func TestRenderJSON(t *testing.T) {
	var b bytes.Buffer
	rows := []ExportRow{{Hour: "2026-06-19T10:00:00Z", Metric: "requests", Country: "US", SumValue: 9}}
	if err := RenderJSON(&b, rows); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var got []ExportRow
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].SumValue != 9 || got[0].Country != "US" {
		t.Errorf("json round-trip = %+v", got)
	}

	// nil renders as [], not null.
	b.Reset()
	if err := RenderJSON(&b, nil); err != nil {
		t.Fatalf("RenderJSON(nil): %v", err)
	}
	if strings.TrimSpace(b.String()) != "[]" {
		t.Errorf("nil JSON = %q, want []", strings.TrimSpace(b.String()))
	}
}
