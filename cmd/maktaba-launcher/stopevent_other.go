//go:build !windows

package main

import "context"

// stopEventName: only Windows has the named stop event; launchd and systemd
// stop the launcher with SIGTERM.
var stopEventName = ""

func watchStopEvent(ctx context.Context, _ string, _ *eventLog) (context.Context, func(), error) {
	return ctx, func() {}, nil
}
