//go:build windows

package main

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

// ServiceName is the optional Windows service (design §2.3); the default on
// Windows is a logon task, which does not use this file.
const ServiceName = "Maktaba"

func isService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// handler runs the launcher loop under the Service Control Manager: stop and
// shutdown end the loop (the server is asked to stop and given StopGrace).
type handler struct{ cfg Config }

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, h.cfg) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1 // the SCM recovery actions restart it
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending, WaitHint: uint32(h.cfg.StopGrace.Milliseconds()) + 5000}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}

func runService(cfg Config) error { return svc.Run(ServiceName, handler{cfg: cfg}) }
