package supervisor

import (
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 53 story 53.12: the log index is pointed at the API's admin /metrics where this
// machine's server.toml puts it, dialled on loopback, and then at streaming's (the TV's
// series); without an admin address, at the registry's ports.
func TestBuildLogsPointsAtTheAdminMetrics(t *testing.T) {
	t.Setenv("MAKTABA_METRICS_URLS", "")
	var cfg config.Config
	cfg.Server.DataDir = t.TempDir()
	cfg.Server.AdminAddr = ":9200"
	p, err := BuildLogs(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Cmd.Env, "MAKTABA_METRICS_URLS=http://127.0.0.1:9200/metrics,http://127.0.0.1:9101/metrics") {
		t.Fatalf("env: no metrics addresses for :9200 and streaming")
	}
	cfg.Server.AdminAddr, cfg.Server.StreamingAdminAddr = "", ":9301"
	if p, err = BuildLogs(cfg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Cmd.Env, "MAKTABA_METRICS_URLS=http://127.0.0.1:9100/metrics,http://127.0.0.1:9301/metrics") {
		t.Fatalf("env: no default admin and :9301")
	}
	for in, want := range map[string]string{":9100": "127.0.0.1:9100", "0.0.0.0:9100": "127.0.0.1:9100",
		"10.0.0.2:9100": "10.0.0.2:9100", "[::]:9100": "127.0.0.1:9100"} {
		if got := dialable(in); got != want {
			t.Errorf("dialable(%q) = %q, want %q", in, got, want)
		}
	}
}
