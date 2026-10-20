package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// RolePostgres is the Postgres bundled with Maktaba.app (Epic 36 story 36.1):
// `postgres` in the foreground on its private loopback port, started before
// every other role (they wait for its check) and stopped after them.
const RolePostgres Role = "postgres"

// ErrStarting from a Check: the process is up but not ready yet (postgres
// recovering after a crash). It is not a failed probe: a long recovery is never
// killed as a hang.
var ErrStarting = errors.New("starting")

// BuildPostgres constructs the postgres role's process for a prepared bundle
// (pgbundle.Bundle.Prepare).
func BuildPostgres(b pgbundle.Bundle) Process {
	cmd := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
	cmd.Env = os.Environ()
	wire(cmd)
	return Process{
		Role:  RolePostgres,
		Cmd:   cmd,
		Probe: "postgres://" + b.Addr(),
		Check: func(ctx context.Context) error {
			err := pgbundle.Ready(ctx, b.Addr(), pgbundle.AppRole, pgbundle.AppDatabase)
			if errors.Is(err, pgbundle.ErrStarting) {
				return fmt.Errorf("%w: %v", ErrStarting, err)
			}
			return err
		},
		StopSignal: os.Interrupt, // fast shutdown: ends sessions, checkpoints, exits
		First:      true,
	}
}
