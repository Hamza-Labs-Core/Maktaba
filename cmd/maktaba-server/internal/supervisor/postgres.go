package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
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
	var shmGate *shmRetryGate
	if runtime.GOOS == "windows" {
		shmGate = newShmRetryGate(b.AwaitShmFree, func() bool { return pgbundle.ShmHeld(b.Data) }, pgbundle.ShmWaitBound)
	}
	p := Process{
		Role:  RolePostgres,
		Cmd:   cmd,
		Probe: "postgres://" + b.Addr(),
		Check: func(ctx context.Context) error {
			err := postgresCheck(pgbundle.Ready(ctx, b.Addr(), pgbundle.AppRole, pgbundle.AppDatabase))
			if err == nil && shmGate != nil {
				shmGate.reset()
			}
			return err
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
	if shmGate != nil {
		// no SIGINT for another process there: pg_ctl's fast shutdown, not a kill
		// (which costs a crash recovery at the next start); unlike the CTRL_BREAK
		// the other roles get, it needs no console
		p.Stop = func() { _ = b.StopCommand().Run() }
		// share one 2-minute deadline across the pre-start AwaitShmFree settle and the
		// at-once retry loop: the lock the pre-start wait goes by is gone with the first
		// failed start, so only Postgres's own exit tells after that
		p.Settle = shmGate.settle
		p.RestartAtOnce = shmGate.retry
	}
	return patient(p), nil
}

// shmRetryGate shares one deadline (bound, pgbundle.ShmWaitBound) across the pre-start
// AwaitShmFree settle and postgres's at-once restart retries on Windows: a start whose
// pre-start wait already spent part (or all) of the bound does not get a second full
// bound on its first failed start. Once Postgres becomes ready (reset) or the shared
// memory is free, the window resets for any future crash.
type shmRetryGate struct {
	await func(context.Context)
	held  func() bool
	bound func() time.Duration
	now   func() time.Time

	mu    sync.Mutex
	until time.Time
}

func newShmRetryGate(await func(context.Context), held func() bool, bound func() time.Duration) *shmRetryGate {
	return &shmRetryGate{await: await, held: held, bound: bound, now: time.Now}
}

func (g *shmRetryGate) settle(ctx context.Context) {
	g.mu.Lock()
	if g.until.IsZero() {
		g.until = g.now().Add(g.bound())
	}
	until := g.until
	g.mu.Unlock()
	wctx, cancel := capDeadline(ctx, until)
	defer cancel()
	g.await(wctx)
}

func (g *shmRetryGate) retry() string {
	if !g.held() {
		g.reset()
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.until.IsZero() {
		g.until = g.now().Add(g.bound())
	}
	if g.now().After(g.until) {
		return ""
	}
	return "the shared memory a postmaster that was killed left is still in use"
}

func (g *shmRetryGate) reset() {
	g.mu.Lock()
	g.until = time.Time{}
	g.mu.Unlock()
}

func capDeadline(ctx context.Context, latest time.Time) (context.Context, context.CancelFunc) {
	if d, ok := ctx.Deadline(); ok && !d.After(latest) {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, latest)
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
