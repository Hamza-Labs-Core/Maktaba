package supervisor

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// RoleCopy is a full node's copy of its home's database (Epic 37, nodecopy): a
// Postgres hot standby on its loopback port. On demand: its manager makes the copy and
// then wants it (and stops it while it copies anew).
const RoleCopy Role = "copy"

// BuildCopy constructs the copy's process.
func BuildCopy(c nodecopy.Copy) Process {
	cmd := exec.Command(c.Postgres(), c.ServerArgs()...)
	cmd.Env = os.Environ()
	wire(cmd)
	p := Process{
		Role:  RoleCopy,
		Cmd:   cmd,
		Probe: "postgres://" + c.Addr(),
		Check: func(ctx context.Context) error {
			return postgresCheck(pgbundle.Ready(ctx, c.Addr(), c.Role(), c.Database()))
		},
		StopSignal: os.Interrupt, // fast shutdown
		OnDemand:   true,
	}
	if runtime.GOOS == "windows" {
		p.Stop = func() { _ = c.StopCommand().Run() }
	}
	return p
}

// Running reports whether a role's process runs (starting, running or unhealthy).
func (s *Supervisor) Running(role Role, instance string) bool {
	for _, c := range s.Snapshot().Processes {
		if c.Role == role && c.Instance == instance {
			return c.State == StateStarting || c.State == StateRunning || c.State == StateUnhealthy
		}
	}
	return false
}
