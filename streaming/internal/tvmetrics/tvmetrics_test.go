package tvmetrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TC1: every metric of design §10 the streaming service owns is exported with
// its labels, in the Prometheus text format.
func TestMetricsRegistered(t *testing.T) {
	Placements.Inc("first_request")
	Failovers.Inc("pc")
	Bytes.Add(1024, "hls", "pc")
	Errors.Inc("pc", "segment")
	PushErrors.Inc("relay_ffmpeg")
	Register(func() []GaugeFamily {
		return []GaugeFamily{
			{Name: HostUp, Help: "up", LabelNames: []string{"host", "role"}, Samples: []Gauge{{Labels: []string{"mac", "home"}, Value: 1}}},
			{Name: HostCapacity, Help: "cap", LabelNames: []string{"host"}, Samples: []Gauge{{Labels: []string{"mac"}, Value: 8}}},
			{Name: HostUsed, Help: "used", LabelNames: []string{"host"}, Samples: []Gauge{{Labels: []string{"mac"}, Value: 2}}},
			{Name: ChannelsOnAir, Help: "air", LabelNames: []string{"host", "encoder"},
				Samples: []Gauge{{Labels: []string{"mac", "videotoolbox"}, Value: 2}}},
		}
	})
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("content type %q", rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{
		`maktaba_tv_host_up{host="mac",role="home"} 1`,
		`maktaba_tv_host_capacity{host="mac"} 8`,
		`maktaba_tv_host_used{host="mac"} 2`,
		`maktaba_tv_channels_on_air{host="mac",encoder="videotoolbox"} 2`,
		`maktaba_tv_placements_total{reason="first_request"} 1`,
		`maktaba_tv_failovers_total{from_host="pc"} 1`,
		`maktaba_tv_frontdoor_bytes_total{kind="hls",host="pc"} 1024`,
		`maktaba_tv_frontdoor_errors_total{host="pc",kind="segment"} 1`,
		`maktaba_tv_remote_push_errors_total{code="relay_ffmpeg"} 1`,
		"# TYPE maktaba_tv_placements_total counter",
		"# TYPE maktaba_tv_host_up gauge",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
}

func TestLabelEscapingAndArity(t *testing.T) {
	v := newVec("counter", "x_total", "x", "a")
	v.Inc("q\"uote\nline")
	v.Inc("too", "many") // ignored: wrong label count
	var b strings.Builder
	v.write(&b)
	if !strings.Contains(b.String(), `x_total{a="q\"uote\nline"} 1`) || strings.Contains(b.String(), "too") {
		t.Fatalf("%s", b.String())
	}
}
