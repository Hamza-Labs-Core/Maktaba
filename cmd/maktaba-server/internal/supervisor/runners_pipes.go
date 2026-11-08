package supervisor

import (
	"io"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

// RunnerPipes are one runner's pipes on Windows (story 54.23), served by the supervisor
// for as long as it runs, under a prefix new to each run:
//
//   - Caller (<prefix><group>): the worker's way to the runner, admitting only this user;
//     each connection is paired with one the runner is waiting with (the broker), or closed
//     without Ready when none waits, so the worker runs the job itself as it does when a
//     runner's socket refuses it on the other systems;
//   - Back (<prefix><group>-back): the runner dials it, from its AppContainer, to wait for
//     a caller: it may not create a pipe, and its Python has no AF_UNIX;
//   - API (<prefix><group>-api): the runner's way to the plugin API, relayed to the API's
//     AF_UNIX socket as the plugin host's relay does (#710).
//
// Back and API admit this user and the group's AppContainer SID (its own profile,
// Maktaba.Runner.<group>) and no other: not another group's runner, not the plugin host.
type RunnerPipes struct {
	Caller, Back, API string
	broker            *pluginrelay.Broker
}

// dial is a connection paired with a waiting runner (the supervisor's health check).
func (p *RunnerPipes) dial() (io.ReadWriteCloser, error) { return p.broker.Take() }
