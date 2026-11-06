package supervisor

import (
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// RoleLogs is the log index (Epic 53 story 53.09): `maktaba-server logs`, the catalog's
// logs.index. It reads the log keeper's segments, so it needs nothing else running; it is
// a process of its own so a corrupt index never takes down the supervisor (README D3).
const RoleLogs Role = "logs"

// LogsAddr is where the log index answers (loopback: the api proxies it to admins):
// [logs] addr, else the port registry's.
func LogsAddr(cfg config.Config) string {
	if a := strings.TrimSpace(cfg.Logs.Addr); a != "" {
		return a
	}
	return ports.LogsIndex.Addr()
}

// dialable is a bind address as a client dials it (":9100" is 127.0.0.1:9100).
func dialable(a string) string {
	host, port, err := net.SplitHostPort(a)
	if err != nil {
		return a
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// BuildLogs is the log index's process: this maktaba-server, `logs --root <logs root>`.
func BuildLogs(cfg config.Config, root string) (Process, error) {
	exe, err := os.Executable()
	if err != nil {
		return Process{}, err
	}
	addr := LogsAddr(cfg)
	cmd := exec.Command(exe, "logs", "--root", root, "--addr", addr)
	cmd.Env = os.Environ()
	// the dashboards scrape the API's admin /metrics, which merges the supervisor's (53.12)
	if a := strings.TrimSpace(cfg.Server.AdminAddr); a != "" && os.Getenv("MAKTABA_METRICS_URLS") == "" {
		cmd.Env = append(cmd.Env, "MAKTABA_METRICS_URLS=http://"+dialable(a)+"/metrics")
	}
	wire(cmd)
	return Process{
		Role:  RoleLogs,
		Cmd:   cmd,
		Probe: "http://" + addr + "/healthz",
		Binds: []ports.Bind{{Addr: addr, What: "search port"}},
	}, nil
}
