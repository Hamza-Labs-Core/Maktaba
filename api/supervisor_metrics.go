package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// supervisorMetricsPrefix: the only families taken from the supervisor's /metrics (its
// update metrics, story 35.15), never one the API has of its own.
const supervisorMetricsPrefix = "maktaba_update_"

// supervisorMetrics gathers this machine's supervisor's /metrics into the API's, so a
// machine is one scrape target (plan 35.15 D2). A supervisor that does not answer (none
// here, an older build, restarting) adds nothing: the API's own numbers still go out.
type supervisorMetrics struct {
	url    string
	client *http.Client
}

// Gather implements prometheus.Gatherer.
func (s supervisorMetrics) Gather() ([]*dto.MetricFamily, error) {
	if s.url == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.url, "/")+"/metrics", nil)
	if err != nil {
		return nil, nil
	}
	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	p := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := p.TextToMetricFamilies(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil
	}
	var out []*dto.MetricFamily
	for name, f := range fams {
		if strings.HasPrefix(name, supervisorMetricsPrefix) {
			out = append(out, f)
		}
	}
	return out, nil
}

// metricsHandler is the API's /metrics: the process's registry and its supervisor's
// update metrics.
func metricsHandler(supervisorURL string) http.Handler {
	return promhttp.HandlerFor(prometheus.Gatherers{metrics.Reg(),
		supervisorMetrics{url: supervisorURL, client: &http.Client{Timeout: 3 * time.Second}}},
		promhttp.HandlerOpts{EnableOpenMetrics: true, ProcessStartTime: time.Now()})
}
