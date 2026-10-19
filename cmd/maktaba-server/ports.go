package main

import (
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// portProbe is the probe setup and serve check binds with (tests replace it).
var portProbe = config.DefaultPortProbe

// reconcilePorts checks the web, API and streaming binds of cfg, moves the ones
// another program holds into the fallback range (config/ports.go), logs each move
// and, when save is set and something moved, writes cfg to path.
func reconcilePorts(cfg *config.Config, path string, o config.PortOptions, out io.Writer, save bool) ([]config.PortChange, error) {
	changes, err := cfg.ReconcilePorts(portProbe(), o)
	for _, ch := range changes {
		fmt.Fprintf(out, "  port: %s\n", ch)
	}
	if err != nil {
		return changes, err
	}
	if save && len(changes) > 0 {
		if err := cfg.SaveTo(path); err != nil {
			return changes, err
		}
		fmt.Fprintf(out, "  port: wrote the new address(es) to %s\n", path)
	}
	return changes, nil
}

// nodeWebPreference is the web port a full node prefers on this OS (0: none).
func nodeWebPreference() int {
	if runtime.GOOS == "windows" {
		return config.WindowsNodeWebPort
	}
	return 0
}

// servePortWait is how long serve waits for a taken port before moving it (a
// previous instance may still be exiting); tests set it to 0.
var servePortWait = 10 * time.Second
