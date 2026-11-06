package components

import (
	"context"
	"errors"
	"net/http"
	"slices"

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

// Restarted is what a restart did: the processes and loops it restarted or started here
// (Restarted: 0 when none of its processes runs now, the supervisor starts them), and the
// hosts asked to restart their loops through a restart request (Requested: streaming, the
// pipeline, or the API from another process). A host asked is not counted: its runner
// restarts the loops it runs when it reads the request, which this cannot confirm.
type Restarted struct {
	Restarted int
	Requested []string
}

// Restart restarts id and records it (component.restart; no desired row changes).
// Refusals, as *svc.Refusal:
//   - not_switchable: part of the server;
//   - not_restartable: nothing to restart (stages, an extension, an asset);
//   - not_running: off or held, or loops of this process only and a requirement is off
//     (its gate shut).
func (x Restarter) Restart(ctx context.Context, id string, actor svc.Actor) (Restarted, error) {
	c, ok := x.Service.Component(id)
	if !ok {
		return Restarted{}, svc.ErrUnknown
	}
	refuse := func(code, detail string) (Restarted, error) {
		return Restarted{}, &svc.Refusal{Status: http.StatusConflict, ID: id, Code: code, Detail: detail}
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
		return Restarted{}, err
	}
	if d.Want != components.WantEnabled || d.Hold != nil {
		return refuse(svc.CodeNotRunning, id+" is off or stopped: start it instead")
	}
	if (procs || supLoops) && x.Packs == nil {
		return Restarted{}, ErrNoSupervisor
	}
	n := 0
	if procs || supLoops {
		if n, err = x.Packs.Restart(ctx, id); err != nil {
			return Restarted{}, &supCallError{err}
		}
	}
	if local {
		m := x.Loops.Restart(id)
		if m == 0 && !procs && !supLoops && len(asked) == 0 {
			return refuse(svc.CodeNotRunning, id+" is waiting: what it requires is off")
		}
		n += m
	}
	requested := []string{}
	for _, h := range asked {
		if _, err := x.Service.Store.RequestRestart(ctx, store.Home, id, h, actor.By); err != nil {
			return Restarted{}, err
		}
		requested = append(requested, h)
	}
	if err := x.Service.Restarted(ctx, id, actor); err != nil {
		return Restarted{}, err
	}
	return Restarted{Restarted: n, Requested: requested}, nil
}
