package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"
)

// T03 (story 53.17): the series have the names and labels of shared/metrics/cloud-series.md,
// and no label names a home, a slug, an account or an address.
func TestTheSeriesNamesAndLabels(t *testing.T) {
	s := New()
	s.Error("cloud.api", "db_timeout")
	s.Error("cloud.api", "")
	s.Request("cloud.relay", "/*", 502, 30*time.Millisecond)
	s.Request("cloud.api", "/v1/homes/{id}/reports", 201, 2*time.Second)
	s.LogLine("cloud.api", "warn")
	s.LogDropped("cloud.api", 3)
	s.ForwardLag("cloud.api", 1500*time.Millisecond)
	s.ProblemReport("created")
	var b bytes.Buffer
	if err := s.WriteMetrics(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`maktaba_errors_total{service="cloud.api",code="db_timeout"} 1`,
		`maktaba_errors_total{service="cloud.api",code="unknown"} 1`,
		`maktaba_http_requests_total{service="cloud.relay",route="/*",status_class="5xx"} 1`,
		`maktaba_http_requests_total{service="cloud.api",route="/v1/homes/{id}/reports",status_class="2xx"} 1`,
		`maktaba_http_request_seconds_bucket{service="cloud.api",route="/v1/homes/{id}/reports",status_class="2xx",le="2.5"} 1`,
		`maktaba_http_request_seconds_bucket{service="cloud.api",route="/v1/homes/{id}/reports",status_class="2xx",le="1"} 0`,
		`maktaba_http_request_seconds_count{service="cloud.relay",route="/*",status_class="5xx"} 1`,
		`maktaba_log_lines_total{service="cloud.api",level="warn"} 1`,
		`maktaba_log_dropped_total{service="cloud.api"} 3`,
		`maktaba_log_forward_lag_seconds{service="cloud.api"} 1.5`,
		`maktaba_problem_reports_total{result="created"} 1`,
		"# TYPE maktaba_http_request_seconds histogram",
		"# TYPE maktaba_log_forward_lag_seconds gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
	labels := regexp.MustCompile(`([a-z_]+)="`).FindAllStringSubmatch(out, -1)
	allowed := map[string]bool{"service": true, "code": true, "route": true, "status_class": true, "le": true,
		"level": true, "result": true}
	for _, l := range labels {
		if !allowed[l[1]] {
			t.Errorf("label %q is not in cloud-series.md (no per-home label)", l[1])
		}
	}
}

// Error lines are counted by their code: the err object's, else the line's, else its event;
// every line by its level.
func TestTheLogHandlerCountsLinesAndErrorsByCode(t *testing.T) {
	s := New()
	var out bytes.Buffer
	l := slog.New(Handler(slog.NewJSONHandler(&out, nil), "cloud.api", s))
	l.Info("hello")
	l.Error("db", "err", slog.GroupValue(slog.String("code", "db_timeout"), slog.String("msg", "x")))
	l.With("code", "relay_dial").Error("dial")
	l.ErrorContext(context.Background(), "plain", "event", "relay_forward_failed")
	l.Error("none")
	var b bytes.Buffer
	_ = s.WriteMetrics(&b)
	for _, want := range []string{
		`maktaba_log_lines_total{service="cloud.api",level="info"} 1`,
		`maktaba_log_lines_total{service="cloud.api",level="error"} 4`,
		`maktaba_errors_total{service="cloud.api",code="db_timeout"} 1`,
		`maktaba_errors_total{service="cloud.api",code="relay_dial"} 1`,
		`maktaba_errors_total{service="cloud.api",code="relay_forward_failed"} 1`,
		`maktaba_errors_total{service="cloud.api",code="unknown"} 1`,
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %s in:\n%s", want, b.String())
		}
	}
	// the lines themselves pass through unchanged
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("%d lines written:\n%s", n, out.String())
	}
}

// A code is a bounded label: what is not a code's characters is dropped, and it is short.
func TestACodeLabelIsBounded(t *testing.T) {
	if got := clip(`a"b{c}=d e/../` + strings.Repeat("x", 100)); strings.ContainsAny(got, `"{}= /`) || len(got) > 64 {
		t.Fatalf("%q", got)
	}
	if clip("€€") != "unknown" {
		t.Fatal("nothing left: unknown")
	}
}
