// Package obs is the cloud's application series for Epic 50.10 (Epic 53 story 53.17 §4; their
// names and labels are fixed in shared/metrics/cloud-series.md): error lines by code, the API's
// and the relay's requests and their latency by route pattern, the log path's own health, and
// the problem reports received. Every label is a fixed or bounded set (a service, a route
// pattern, a status class, an error code, a level): no home, slug, account or address is ever
// a label. Each role serves them on its /metrics (WriteMetrics).
package obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// Buckets of maktaba_http_request_seconds.
var Buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type hist struct {
	counts []int64 // per bucket, cumulative when written
	sum    float64
	n      int64
}

// Series is one process's series.
type Series struct {
	mu      sync.Mutex
	errors  map[[2]string]int64 // service, code
	reqs    map[[3]string]int64 // service, route, status class
	secs    map[[3]string]*hist
	lines   map[[2]string]int64 // service, level
	dropped map[string]int64    // service
	lag     map[string]float64  // service
	reports map[string]int64    // result
}

// New is an empty set of series.
func New() *Series {
	return &Series{errors: map[[2]string]int64{}, reqs: map[[3]string]int64{}, secs: map[[3]string]*hist{},
		lines: map[[2]string]int64{}, dropped: map[string]int64{}, lag: map[string]float64{}, reports: map[string]int64{}}
}

// Default is the process's series (each role's /metrics writes it).
var Default = New()

var (
	serviceMu sync.Mutex
	service   = "cloud"
)

// SetService names this process's service (cloud.api, cloud.relay, cloud.worker, cloud.logs).
func SetService(s string) {
	serviceMu.Lock()
	service = s
	serviceMu.Unlock()
}

// Service is this process's service.
func Service() string {
	serviceMu.Lock()
	defer serviceMu.Unlock()
	return service
}

// Error counts one error line of service with code ("unknown" when it has none).
func (s *Series) Error(svc, code string) {
	if code == "" {
		code = "unknown"
	}
	s.mu.Lock()
	s.errors[[2]string{svc, clip(code)}]++
	s.mu.Unlock()
}

// Request counts one request: its route pattern (never a path: "/v1/homes/{id}"), its status
// class and how long it took.
func (s *Series) Request(svc, route string, status int, d time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	k := [3]string{svc, route, fmt.Sprintf("%dxx", status/100)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs[k]++
	h := s.secs[k]
	if h == nil {
		h = &hist{counts: make([]int64, len(Buckets))}
		s.secs[k] = h
	}
	sec := d.Seconds()
	for i, b := range Buckets {
		if sec <= b {
			h.counts[i]++
		}
	}
	h.sum += sec
	h.n++
}

// LogLine counts one line written.
func (s *Series) LogLine(svc, level string) {
	s.mu.Lock()
	s.lines[[2]string{svc, level}]++
	s.mu.Unlock()
}

// LogDropped counts lines the log path dropped (a full spool, a full disk).
func (s *Series) LogDropped(svc string, n int64) {
	s.mu.Lock()
	s.dropped[svc] += n
	s.mu.Unlock()
}

// ForwardLag sets how far behind the forwarding to the witness is.
func (s *Series) ForwardLag(svc string, d time.Duration) {
	s.mu.Lock()
	s.lag[svc] = d.Seconds()
	s.mu.Unlock()
}

// ProblemReport counts one problem report a home sent (created, duplicate, refused).
func (s *Series) ProblemReport(result string) {
	s.mu.Lock()
	s.reports[result]++
	s.mu.Unlock()
}

// WriteMetrics writes the series as Prometheus text (a PrometheusHandler extra).
func (s *Series) WriteMetrics(w io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	head := func(name, kind, help string) { fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind) }

	head("maktaba_errors_total", "counter", "Error lines, by service and error code.")
	for _, k := range sorted2(s.errors) {
		fmt.Fprintf(&b, "maktaba_errors_total{service=%q,code=%q} %d\n", k[0], k[1], s.errors[k])
	}
	head("maktaba_http_requests_total", "counter", "Requests to the API and the relay, by route pattern and status class.")
	keys := sorted3(s.reqs)
	for _, k := range keys {
		fmt.Fprintf(&b, "maktaba_http_requests_total{service=%q,route=%q,status_class=%q} %d\n", k[0], k[1], k[2], s.reqs[k])
	}
	head("maktaba_http_request_seconds", "histogram", "Request latency, by route pattern and status class.")
	for _, k := range keys {
		h := s.secs[k]
		lbl := fmt.Sprintf("service=%q,route=%q,status_class=%q", k[0], k[1], k[2])
		for i, bound := range Buckets {
			fmt.Fprintf(&b, "maktaba_http_request_seconds_bucket{%s,le=%q} %d\n", lbl, trimFloat(bound), h.counts[i])
		}
		fmt.Fprintf(&b, "maktaba_http_request_seconds_bucket{%s,le=\"+Inf\"} %d\n", lbl, h.n)
		fmt.Fprintf(&b, "maktaba_http_request_seconds_sum{%s} %g\n", lbl, h.sum)
		fmt.Fprintf(&b, "maktaba_http_request_seconds_count{%s} %d\n", lbl, h.n)
	}
	head("maktaba_log_lines_total", "counter", "Log lines written, by service and level.")
	for _, k := range sorted2(s.lines) {
		fmt.Fprintf(&b, "maktaba_log_lines_total{service=%q,level=%q} %d\n", k[0], k[1], s.lines[k])
	}
	head("maktaba_log_dropped_total", "counter", "Log lines the log path dropped.")
	for _, k := range sorted1(s.dropped) {
		fmt.Fprintf(&b, "maktaba_log_dropped_total{service=%q} %d\n", k, s.dropped[k])
	}
	head("maktaba_log_forward_lag_seconds", "gauge", "How far behind the forwarding of logs to the witness is.")
	for _, k := range sortedF(s.lag) {
		fmt.Fprintf(&b, "maktaba_log_forward_lag_seconds{service=%q} %g\n", k, s.lag[k])
	}
	head("maktaba_problem_reports_total", "counter", "Problem reports homes sent, by result.")
	for _, k := range sorted1(s.reports) {
		fmt.Fprintf(&b, "maktaba_problem_reports_total{result=%q} %d\n", k, s.reports[k])
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteMetrics writes the process's series.
func WriteMetrics(w io.Writer) error { return Default.WriteMetrics(w) }

// Handler counts every line of next by level, and each error line by its code (53.01: the
// err object's code, else the line's code, else its event), then passes it on.
func Handler(next slog.Handler, svc string, s *Series) slog.Handler {
	return &countHandler{next: next, svc: svc, s: s}
}

type countHandler struct {
	next  slog.Handler
	svc   string
	s     *Series
	attrs []slog.Attr
}

func (h *countHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *countHandler) Handle(ctx context.Context, r slog.Record) error {
	h.s.LogLine(h.svc, levelName(r.Level))
	if r.Level >= slog.LevelError {
		code, event := "", ""
		look := func(a slog.Attr) {
			switch {
			case a.Key == "code" && code == "":
				code = a.Value.String()
			case a.Key == "event" && event == "":
				event = a.Value.String()
			case a.Key == "err" && a.Value.Kind() == slog.KindGroup:
				for _, g := range a.Value.Group() {
					if g.Key == "code" {
						code = g.Value.String()
					}
				}
			}
		}
		for _, a := range h.attrs {
			look(a)
		}
		r.Attrs(func(a slog.Attr) bool { look(a); return true })
		if code == "" {
			code = event
		}
		h.s.Error(h.svc, code)
	}
	return h.next.Handle(ctx, r)
}

func (h *countHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &countHandler{next: h.next.WithAttrs(as), svc: h.svc, s: h.s, attrs: append(append([]slog.Attr{}, h.attrs...), as...)}
}

func (h *countHandler) WithGroup(name string) slog.Handler {
	return &countHandler{next: h.next.WithGroup(name), svc: h.svc, s: h.s, attrs: h.attrs}
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

// clip keeps a code label short and of a code's characters (a bounded label).
func clip(code string) string {
	var b strings.Builder
	for _, c := range code {
		if b.Len() >= 64 {
			break
		}
		if c == '_' || c == '.' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", f), "0"), ".")
}

func sorted1[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedF(m map[string]float64) []string { return sorted1(m) }

func sorted2(m map[[2]string]int64) [][2]string {
	out := make([][2]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0]+"\x00"+out[i][1] < out[j][0]+"\x00"+out[j][1] })
	return out
}

func sorted3(m map[[3]string]int64) [][3]string {
	out := make([][3]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join(out[i][:], "\x00") < strings.Join(out[j][:], "\x00")
	})
	return out
}
