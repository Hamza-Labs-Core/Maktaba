package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func TestPolicyDefaults(t *testing.T) {
	t.Parallel()
	p, err := PolicyFromConfig(config.SupervisorSection{}) // [supervisor] absent
	if err != nil {
		t.Fatal(err)
	}
	want := Policy{
		Restart: RestartAlways, BackoffInitial: time.Second, BackoffMax: time.Minute,
		HealthyAfter: 5 * time.Minute, MaxRestarts: 5, Window: 10 * time.Minute,
		FailedRetry: 5 * time.Minute, ProbeInterval: 10 * time.Second, ProbeTimeout: 3 * time.Second,
		ProbeFailures: 3, StartupGrace: time.Minute, AdminAddr: "127.0.0.1:9103",
	}
	if p != want {
		t.Fatalf("defaults = %+v\nwant       %+v", p, want)
	}
	if DefaultPolicy() != want {
		t.Fatal("DefaultPolicy differs from an empty section")
	}
}

func TestPolicyOverridesAndErrors(t *testing.T) {
	t.Parallel()
	p, err := PolicyFromConfig(config.SupervisorSection{Restart: "On-Failure", BackoffMax: "2m", AdminAddr: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Restart != RestartOnFailure || p.BackoffMax != 2*time.Minute || p.AdminAddr != "" {
		t.Fatalf("overrides = %+v", p)
	}

	_, err = PolicyFromConfig(config.SupervisorSection{
		Restart: "sometimes", BackoffInitial: "fast", MaxRestarts: -1, ProbeInterval: "0s",
	})
	if err == nil {
		t.Fatal("bad values accepted")
	}
	for _, key := range []string{"supervisor.restart", "supervisor.backoff_initial", "supervisor.max_restarts", "supervisor.probe_interval"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}
	if _, err := PolicyFromConfig(config.SupervisorSection{BackoffInitial: "2m", BackoffMax: "1m"}); err == nil {
		t.Error("backoff_max below backoff_initial accepted")
	}
}

func TestNextBackoff(t *testing.T) {
	t.Parallel()
	d := time.Second
	var got []time.Duration
	for i := 0; i < 8; i++ {
		got = append(got, d)
		d = nextBackoff(d, time.Minute)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("backoff sequence = %v", got)
		}
	}
}

func TestConfigLoadsSupervisorSection(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	if cfg.Supervisor != config.DefaultSupervisor() {
		t.Fatalf("Defaults().Supervisor = %+v", cfg.Supervisor)
	}
}

func TestProbeURLsAndStateURL(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Server.AdminAddr = "0.0.0.0:9200"
	if got := ProbeURL(RoleAPI, cfg); got != "http://127.0.0.1:9200/healthz" {
		t.Errorf("api probe = %q", got)
	}
	if got := ProbeURL(RoleStreaming, cfg); got != "http://127.0.0.1:9101/healthz" {
		t.Errorf("streaming probe = %q", got)
	}
	if got := ProbeURL(RolePipeline, cfg); got != "http://127.0.0.1:9102/healthz" {
		t.Errorf("pipeline probe = %q", got)
	}
	if !hasKV(childEnvOK(RoleAPI, cfg), "MAKTABA_SUPERVISOR_URL", "http://127.0.0.1:9103") {
		t.Error("api role missing MAKTABA_SUPERVISOR_URL")
	}
	cfg.Supervisor.AdminAddr = "off"
	if hasKey(childEnvOK(RoleAPI, cfg), "MAKTABA_SUPERVISOR_URL") {
		t.Error("MAKTABA_SUPERVISOR_URL set with admin_addr = off")
	}
}
