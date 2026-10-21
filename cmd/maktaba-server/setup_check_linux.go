package main

import (
	"context"
	"runtime"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostcheck"
)

// linuxCheck reads this machine (tests replace it).
var linuxCheck = func(ctx context.Context) hostcheck.Result {
	sd := newServiceDeps()
	l := hostcheck.LocalLinux(runtime.GOARCH, sd.unit)
	l.Manager = sd.manager
	return l.Check(ctx)
}

// platformCheck adds the Linux part of `check` (Epic 39 story 39.6): glibc,
// the systemd user manager the server runs under, linger, and a
// maktaba-server.service another install owns. Problems block the setup (ok
// false) before it builds a venv of several GB that could never run.
func platformCheck(ctx context.Context, res *checkResult) {
	r := linuxCheck(ctx)
	if len(r.Problems) > 0 {
		res.OK = false
	}
	res.Warnings = append(res.Warnings, r.Problems...)
	res.Warnings = append(res.Warnings, r.Warnings...)
}
