package main

import (
	"context"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostcheck"
)

// linuxCheck reads this machine (tests replace it).
var linuxCheck = func(ctx context.Context) hostcheck.Result {
	sd := newServiceDeps()
	l := hostcheck.LocalLinux(sd.unit)
	l.Manager = sd.manager
	return l.Check(ctx)
}

// The Linux part of `check` (Epic 39 story 39.6): glibc, the systemd user
// manager the server runs under, linger, and a maktaba-server.service another
// install owns. A problem blocks the setup (ok false) before it builds a venv
// of several GB that could never run.
func init() { platformChecks = append(platformChecks, checkLinux) }

func checkLinux(ctx context.Context, res *checkResult) {
	r := linuxCheck(ctx)
	for _, n := range r.Problems {
		res.Problem(n.Code, n.Message, n.Params...)
	}
	for _, n := range r.Warnings {
		res.Warn(n.Code, n.Message, n.Params...)
	}
}
