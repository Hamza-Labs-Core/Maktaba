package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
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
// (pgbundle.Bundle.Prepare): its ServerCommand (a restricted token for an
// administrator on Windows).
func BuildPostgres(b pgbundle.Bundle) (Process, error) {
	cmd, err := b.ServerCommand()
	if err != nil {
		return Process{}, err
	}
	cmd.Env = os.Environ()
	wire(cmd)
	p := Process{
		Role:  RolePostgres,
		Cmd:   cmd,
		Probe: "postgres://" + b.Addr(),
		Check: func(ctx context.Context) error {
			return postgresCheck(pgbundle.Ready(ctx, b.Addr(), pgbundle.AppRole, pgbundle.AppDatabase))
		},
		StopSignal: os.Interrupt, // fast shutdown: ends sessions, checkpoints, exits
		First:      true,
		// whether it listens beyond this machine, at each start (pgbundle.WatchReach)
		Args:        b.ServerArgs,
		BeforeStart: b.ClearStaleLock,
		Settle:      b.AwaitShmFree,
		// checked after BeforeStart, which ends a postmaster a killed server left
		Binds: []ports.Bind{{Addr: b.Addr(), What: "database port"}},
	}
	if runtime.GOOS == "windows" {
		// no SIGINT for another process there: pg_ctl's fast shutdown, not a kill
		// (which costs a crash recovery at the next start); unlike the CTRL_BREAK
		// the other roles get, it needs no console
		p.Stop = func() { _ = b.StopCommand().Run() }
	}
	return patient(p), nil
}

// The bounds of a Postgres the supervisor runs (the home's, a node's copy). Stopping one
// ends every connection, and killing one costs a crash recovery and can leave backends
// holding its shared memory, next to which no new one starts: both are far worse than
// waiting out a slow spell. The home's was stopped as hung, and killed, when it was slow
// for about a minute, most often in the middle of a shutdown it had been asked for.
const (
	// PostgresProbeTimeout is how long one liveness check waits for the postmaster's
	// answer: it forks a backend to give it, which takes seconds on a busy machine.
	PostgresProbeTimeout = 15 * time.Second
	// PostgresHangAfter is how long its checks must fail in a row before it is taken for
	// hung and asked to shut down. An answer of any kind in that time (ready, starting
	// up, recovering, shutting down, too many clients) starts the count again.
	PostgresHangAfter = 5 * time.Minute
	// PostgresStopGrace is how long its fast shutdown (ending the sessions, then a
	// checkpoint of everything not yet written) has before it is killed: at a restart,
	// at the server's stop and after a hang. 30 s was too short on a busy disk.
	PostgresStopGrace = 2 * time.Minute
)

// patient gives a Postgres process those bounds.
func patient(p Process) Process {
	p.ProbeTimeout, p.HangAfter, p.StopGrace = PostgresProbeTimeout, PostgresHangAfter, PostgresStopGrace
	return p
}

// postgresCheck maps the readiness answer onto the liveness check: starting up
// or recovering is ErrStarting; any other answer of the server (too many
// clients) proves it alive, so a busy database is never killed as a hang.
func postgresCheck(err error) error {
	switch {
	case errors.Is(err, pgbundle.ErrStarting):
		return fmt.Errorf("%w: %v", ErrStarting, err)
	case errors.Is(err, pgbundle.ErrRefused):
		return nil
	}
	return err
}
