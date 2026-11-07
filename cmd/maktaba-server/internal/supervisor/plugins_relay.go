package supervisor

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

var (
	relayOnce sync.Once
	relayPipe string
)

// PluginsPipe is the pipe the pipeline reaches the plugin host through on Windows (#710):
// the supervisor's relay to the host's AF_UNIX dispatch socket, started once, the first
// time a pipeline's environment is built, under a name new to this run. "" elsewhere, and
// when the relay could not start (logged): the pipeline then finds no pipe and its
// dispatcher takes the host as unavailable, as when the socket is missing.
func PluginsPipe(cfg config.Config) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	relayOnce.Do(func() {
		warn := func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[serve] level=warn "+format+"\n", args...)
		}
		name, err := pluginrelay.PipeName()
		if err != nil {
			warn("the plugin host's relay: %v", err)
			return
		}
		l, err := pluginrelay.Listen(name)
		if err != nil {
			warn("the plugin host's relay: %v", err)
			return
		}
		dispatch := PluginsPaths(cfg).Dispatch
		go func() {
			err := pluginrelay.Serve(l, func() (net.Conn, error) {
				return net.DialTimeout("unix", dispatch, 5*time.Second)
			}, warn)
			if err != nil {
				warn("the plugin host's relay stopped: %v", err)
			}
		}()
		relayPipe = name
	})
	return relayPipe
}
