package supervisor

import (
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

// BuildLogs is the log index's process: this maktaba-server, `logs --root <logs root>`.
func BuildLogs(cfg config.Config, root string) (Process, error) {
	exe, err := os.Executable()
	if err != nil {
		return Process{}, err
	}
	addr := LogsAddr(cfg)
	cmd := exec.Command(exe, "logs", "--root", root, "--addr", addr)
	cmd.Env = os.Environ()
	wire(cmd)
	return Process{
		Role:  RoleLogs,
		Cmd:   cmd,
		Probe: "http://" + addr + "/healthz",
		Binds: []ports.Bind{{Addr: addr, What: "search port"}},
	}, nil
}
