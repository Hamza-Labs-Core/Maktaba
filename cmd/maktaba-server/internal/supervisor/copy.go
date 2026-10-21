package supervisor

import (
	"context"
	"os"
	"runtime"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// RoleCopy is a full node's copy of its home's database (Epic 37, nodecopy): this
// machine's own Postgres on its loopback port, subscribed to the home's publication.
// On demand: its manager makes the database first, then wants it.
const RoleCopy Role = "copy"

// BuildCopy constructs the copy's process: its ServerCommand (a restricted token for an
// administrator on Windows, as the home's). The copy's programs are known (c.DB.Home)
// before its manager runs: they are this machine's install, or the pinned build's place.
func BuildCopy(c nodecopy.Copy) (Process, error) {
	cmd, err := c.DB.ServerCommand()
	if err != nil {
		return Process{}, err
	}
	cmd.Env = os.Environ()
	wire(cmd)
	p := Process{
		Role:  RoleCopy,
		Cmd:   cmd,
		Probe: "postgres://" + c.Addr(),
		Check: func(ctx context.Context) error {
			return postgresCheck(pgbundle.Ready(ctx, c.Addr(), pgbundle.AppRole, pgbundle.AppDatabase))
		},
		StopSignal: os.Interrupt, // fast shutdown
		OnDemand:   true,
	}
	if runtime.GOOS == "windows" {
		p.Stop = func() { _ = c.DB.StopCommand().Run() }
	}
	return p, nil
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
