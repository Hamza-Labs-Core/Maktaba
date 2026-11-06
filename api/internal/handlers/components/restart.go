package components

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// ErrNoSupervisor: a restart of a component with processes, on a server that runs under
// no supervisor.
var ErrNoSupervisor = errors.New("components: this server runs under no supervisor")

// supCallError is a restart's failed call to the supervisor (it does not answer, or it
// refuses): the route answers it as the packs routes do.
type supCallError struct{ err error }

func (e *supCallError) Error() string { return e.err.Error() }
func (e *supCallError) Unwrap() error { return e.err }

// Restarter restarts a running component (story 51.08 §2), for the admin route and the
// CLI alike: its processes and the supervisor's own loops of it through this machine's
// supervisor, its loops in Loops (the API's own runner) cancelled and started again, and
// its loops in any other process (streaming, the pipeline, or the API when Loops is not
// it) through a restart request that process's runner reads (slot 0185, #636).
type Restarter struct {
	Service *svc.Service
	// Packs is this machine's supervisor; nil: none, so a component with processes or
	// supervisor loops is ErrNoSupervisor.
	Packs *Packs
	// Loops is the API's runner; nil (the CLI, another process): the API's loops are
	// asked for through a request too.
	Loops *runner.Runner
}

// hostSupervisor is the catalog's host of the supervisor's own loops.
const hostSupervisor = "supervisor"

// Restart restarts id and records it (component.restart; no desired row changes). It
// answers how many processes and loops it restarted, started or asked another process to
// restart: 0 when none of its processes runs now (the supervisor starts them). Refusals,
// as *svc.Refusal:
//   - not_switchable: part of the server;
//   - not_restartable: nothing to restart (stages, an extension, an asset);
//   - not_running: off or held, or loops of this process only and a requirement is off
//     (its gate shut).
func (x Restarter) Restart(ctx context.Context, id string, actor svc.Actor) (int, error) {
	c, ok := x.Service.Component(id)
	if !ok {
		return 0, svc.ErrUnknown
	}
	refuse := func(code, detail string) (int, error) {
		return 0, &svc.Refusal{Status: http.StatusConflict, ID: id, Code: code, Detail: detail}
	}
	if !c.Switchable {
		return refuse(svc.CodeNotSwitchable, id+" is part of the server")
	}
	procs := c.HasPart(components.KindProcess)
	hosts := runner.Hosts(c)
	local := x.Loops != nil && slices.Contains(hosts, x.Loops.Host())
	supLoops := slices.Contains(hosts, hostSupervisor)
	var asked []string // the hosts asked through a restart request
	for _, h := range hosts {
		if h != hostSupervisor && (!local || h != x.Loops.Host()) {
			asked = append(asked, h)
		}
	}
	if !procs && len(hosts) == 0 {
		return refuse(svc.CodeNotRestartable, id+" is a "+c.Kind+": it has no process or loop to restart")
	}
	d, err := x.Service.Show(ctx, id)
	if err != nil {
		return 0, err
	}
	if d.Want != components.WantEnabled || d.Hold != nil {
		return refuse(svc.CodeNotRunning, id+" is off or stopped: start it instead")
	}
	if (procs || supLoops) && x.Packs == nil {
		return 0, ErrNoSupervisor
	}
	n := 0
	if procs || supLoops {
		if n, err = x.Packs.Restart(ctx, id); err != nil {
			return 0, &supCallError{err}
		}
	}
	if local {
		m := x.Loops.Restart(id)
		if m == 0 && !procs && !supLoops && len(asked) == 0 {
			return refuse(svc.CodeNotRunning, id+" is waiting: what it requires is off")
		}
		n += m
	}
	for _, h := range asked {
		if _, err := x.Service.Store.RequestRestart(ctx, store.Home, id, h, actor.By); err != nil {
			return 0, err
		}
		n += loopsIn(c, h)
	}
	if err := x.Service.Restarted(ctx, id, actor); err != nil {
		return 0, err
	}
	return n, nil
}

// loopsIn is how many of c's loops run in host.
func loopsIn(c *components.Component, host string) int {
	n := 0
	for _, l := range c.Loops {
		if h, _, _ := strings.Cut(l, ":"); h == host {
			n++
		}
	}
	return n
}
