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
// CLI alike: its processes through this machine's supervisor, its loops in Loops (the
// API's own runner) cancelled and started again.
type Restarter struct {
	Service *svc.Service
	// Packs is this machine's supervisor; nil: none, so a component with processes is
	// ErrNoSupervisor.
	Packs *Packs
	// Loops is the API's runner; nil (the CLI, another process): no loop restarts here.
	Loops *runner.Runner
}

// Restart restarts id and records it (component.restart; no desired row changes). It
// answers how many processes and loops it restarted or started: 0 when none of its
// processes runs now (the supervisor starts them). Refusals, as *svc.Refusal:
//   - not_switchable: part of the server;
//   - not_restartable: nothing here to restart (stages, an extension, an asset, or loops
//     that all run in another process: that process's runner owns them);
//   - not_running: off or held, or loops only and a requirement is off (its gate shut).
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
	loops := c.HasPart(components.KindLoop) && x.Loops != nil && slices.Contains(hosts, x.Loops.Host())
	if !procs && !loops {
		if len(hosts) > 0 {
			return refuse(svc.CodeNotRestartable, id+"'s loops run in "+strings.Join(hosts, ", ")+
				", and only that process's runner restarts them")
		}
		return refuse(svc.CodeNotRestartable, id+" is a "+c.Kind+": it has no process or loop to restart")
	}
	d, err := x.Service.Show(ctx, id)
	if err != nil {
		return 0, err
	}
	if d.Want != components.WantEnabled || d.Hold != nil {
		return refuse(svc.CodeNotRunning, id+" is off or stopped: start it instead")
	}
	if procs && x.Packs == nil {
		return 0, ErrNoSupervisor
	}
	n := 0
	if procs {
		if n, err = x.Packs.Restart(ctx, id); err != nil {
			return 0, &supCallError{err}
		}
	}
	if loops {
		m := x.Loops.Restart(id)
		if m == 0 && !procs {
			return refuse(svc.CodeNotRunning, id+" is waiting: what it requires is off")
		}
		n += m
	}
	if err := x.Service.Restarted(ctx, id, actor); err != nil {
		return 0, err
	}
	return n, nil
}
