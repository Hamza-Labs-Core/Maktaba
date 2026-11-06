package config

import (
	"testing"
	"time"
)

// Epic 53 stories 53.02, 53.10, 53.11: [logs]' defaults are 7 days of logs, 30 of error
// groups, 90 of problem reports and 1 GB in all; a value out of its range takes the default.
func TestLogsResolved(t *testing.T) {
	d := LogsSection{}.Resolved()
	day := 24 * time.Hour
	if d.Retention != 7*day || d.ErrorRetention != 30*day || d.ReportRetention != 90*day || d.MaxTotal != 1<<30 {
		t.Fatalf("defaults: %+v", d)
	}
	r := LogsSection{Retention: "14d", ErrorRetention: "60d", ReportRetention: "180d", MaxTotalSize: "2GB"}.Resolved()
	if r.Retention != 14*day || r.ErrorRetention != 60*day || r.ReportRetention != 180*day || r.MaxTotal != 2<<30 {
		t.Fatalf("set: %+v", r)
	}
	o := LogsSection{Retention: "91d", ErrorRetention: "400d", ReportRetention: "0d", MaxTotalSize: "100MB"}.Resolved()
	if o.Retention != 7*day || o.ErrorRetention != 30*day || o.ReportRetention != 90*day || o.MaxTotal != 1<<30 {
		t.Fatalf("out of range: %+v", o)
	}
}
