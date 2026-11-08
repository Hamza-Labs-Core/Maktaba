//go:build !windows

package supervisor

import (
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

// runnerPipesFor is Windows only: elsewhere a runner listens on its own unix socket.
func runnerPipesFor(config.Config, string) (*RunnerPipes, error) {
	return nil, pluginrelay.ErrUnsupported
}

// RunnerPipePrefix is Windows only ("" elsewhere).
func RunnerPipePrefix() string { return "" }
