//go:build windows

package main

import (
	"context"

	"golang.org/x/sys/windows"
)

// StopEventName is the manual-reset event that stops an app-bundle launcher
// cleanly (Epic 38 contract-agent, story 38.2). The app's agent creates and
// resets it before it starts the launcher; Pause, Restart and --server-stop
// set it. The shell and the agent are GUI programs outside the launcher's
// windowless console, so they cannot send it CTRL_C; one SetEvent from any
// process of the user's session does the same.
const StopEventName = `Local\Maktaba.Server.Stop`

// stopEventName is the event app-bundle mode watches (tests give theirs a
// name of their own, so they never meet a Maktaba this PC runs).
var stopEventName = StopEventName

// watchStopEvent returns a context that ends when the named event is
// signalled, as a CTRL_C would: Run's stop path then writes the stop file,
// sends the server CTRL_BREAK and kills it after StopGrace. The event is
// opened, or created when the agent did not make it, and never reset (the
// agent owns that). An event set already, by a Pause before this launcher
// started, ends the context at once, so a paused server is not started.
// The returned func ends the waiting goroutine and closes the handles; Run
// calls it before it returns, so nothing outlives the run.
func watchStopEvent(parent context.Context, name string, log *eventLog) (context.Context, func(), error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return parent, func() {}, err
	}
	// SYNCHRONIZE is all a wait needs (an event the agent made with a stricter
	// DACL still opens); the flags apply only when this call creates it.
	ev, err := windows.CreateEventEx(nil, p, windows.CREATE_EVENT_MANUAL_RESET, windows.SYNCHRONIZE)
	if ev == 0 {
		return parent, func() {}, err
	}
	quit, err := windows.CreateEvent(nil, 1, 0, nil) // ends the wait when the run ends first
	if quit == 0 {
		_ = windows.CloseHandle(ev)
		return parent, func() {}, err
	}
	ctx, cancel := context.WithCancel(parent)
	fired := func() {
		log.Event("launcher_stop_event", "", map[string]any{"name": name})
		cancel()
	}
	done := make(chan struct{})
	if s, _ := windows.WaitForSingleObject(ev, 0); s == windows.WAIT_OBJECT_0 {
		fired()
		close(done)
	} else {
		go func() {
			defer close(done)
			s, err := windows.WaitForMultipleObjects([]windows.Handle{ev, quit}, false, windows.INFINITE)
			switch {
			case s == windows.WAIT_OBJECT_0:
				fired()
			case err != nil: // WAIT_FAILED: the launcher runs on, stoppable as before
				log.Event("launcher_stop_event_error", "", map[string]any{"name": name, "err": err.Error()})
			}
		}()
	}
	release := func() {
		_ = windows.SetEvent(quit)
		<-done
		cancel()
		_ = windows.CloseHandle(quit)
		_ = windows.CloseHandle(ev)
	}
	return ctx, release, nil
}
