//go:build windows

package supervisor

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appcontainer"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

// RunnerWait is how long a caller waits for a runner connection to be paired with (one is
// opened again as soon as the last was taken; none waits while the runner is not up).
var RunnerWait = 2 * time.Second

var (
	prefixOnce   sync.Once
	runnerPrefix string

	runnerPipesMu sync.Mutex
	runnerPipes   = map[string]*RunnerPipes{}
)

// RunnerPipePrefix is this run's prefix of the runners' pipes (EnvRunnerPipes for the
// worker): 128 random bits, never reused.
func RunnerPipePrefix() string {
	prefixOnce.Do(func() {
		var b [16]byte
		if _, err := rand.Read(b[:]); err == nil {
			runnerPrefix = `\\.\pipe\maktaba-runner-` + hex.EncodeToString(b[:]) + "-"
		}
	})
	return runnerPrefix
}

// runnerPipesFor serves a runner group's pipes, once per run of the supervisor (a runner
// that restarts dials the same ones).
func runnerPipesFor(cfg config.Config, instance string) (*RunnerPipes, error) {
	apiSock := PluginsPaths(cfg).API
	runnerPipesMu.Lock()
	defer runnerPipesMu.Unlock()
	// one config per run; a test that builds runners for another data folder gets its own
	key := instance + "\x00" + apiSock
	if p, ok := runnerPipes[key]; ok {
		return p, nil
	}
	prefix := RunnerPipePrefix()
	if prefix == "" {
		return nil, errors.New("no random pipe name")
	}
	sid, err := appcontainer.SIDString(appcontainer.RunnerProfile(instance))
	if err != nil {
		return nil, err
	}
	name := prefix + instance
	if n := len(runnerPipes); n > 0 {
		for k := range runnerPipes {
			if strings.HasPrefix(k, instance+"\x00") {
				name = prefix + instance + "-" + strconv.Itoa(n)
				break
			}
		}
	}
	p := &RunnerPipes{Caller: name, Back: name + "-back", API: name + "-api", broker: pluginrelay.NewBroker(RunnerWait)}
	caller, err := pluginrelay.Listen(p.Caller)
	if err != nil {
		return nil, err
	}
	back, err := pluginrelay.ListenFor(p.Back, sid)
	if err != nil {
		_ = caller.Close()
		return nil, err
	}
	api, err := pluginrelay.ListenFor(p.API, sid)
	if err != nil {
		_ = caller.Close()
		_ = back.Close()
		return nil, err
	}
	go func() {
		for {
			c, err := caller.Accept()
			if err != nil {
				warnf("the plugin runner %s's pipe stopped: %v", instance, err)
				return
			}
			go p.broker.Serve(c)
		}
	}()
	go func() {
		for {
			c, err := back.Accept()
			if err != nil {
				warnf("the plugin runner %s's back pipe stopped: %v", instance, err)
				return
			}
			p.broker.Offer(c)
		}
	}()
	go func() {
		err := pluginrelay.Serve(api, func() (net.Conn, error) {
			return net.DialTimeout("unix", apiSock, 5*time.Second)
		}, warnf)
		if err != nil {
			warnf("the plugin runner %s's way to the plugin API stopped: %v", instance, err)
		}
	}()
	runnerPipes[key] = p
	return p, nil
}
