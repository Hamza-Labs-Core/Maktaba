package main

import (
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The TV handover across a supervisor restart (Epic 35 story 35.7, design §8.1). The
// server keeps <updates>/handover.json, the record of the TV processes that may outlive
// it (layout.Handover). When it exits 75 having handed them over (the record says so,
// written by that server on this boot) they run on, and the next server adopts them:
// the launcher leaves them alone. So it does when a release under trial (pending) ends
// any other way, a crash or the guard's timeout: the next start (the release again, or
// the one the guard goes back to) adopts them, when both releases' supervisors adopt
// (their release.json's handover_protocol). On any other exit, and on a stop, it stops
// them and removes the record (the server stops them itself on a stop; this is for one
// that could not: killed after the grace, crashed).

// survivors deals with the record once the server (pid) has exited; restart: it exited
// 75; pending: the release it ran is under trial. It returns the process groups the
// launcher keeps of what the server left behind (Linux: they are its orphans, which it
// otherwise stops), nil when it keeps none.
func (ln *launcher) survivors(pid int, restart, pending bool) map[int]bool {
	dir := ln.l.UpdatesDir()
	rec, err := layout.ReadHandover(dir)
	if err != nil {
		ln.log.Event("launcher_handover_unreadable", "", map[string]any{"err": err.Error()})
		_ = layout.RemoveHandover(dir)
		stopTVJob(dir)
		return nil
	}
	if restart && rec.HandedOverBy(pid) {
		ln.log.Event("launcher_handover", "", map[string]any{"processes": len(rec.Children)})
		return keepOf(rec)
	}
	if pending && rec != nil && len(rec.Children) > 0 && rec.CurrentBoot() && ln.nextAdopts() {
		ln.log.Event("launcher_handover_kept", "", map[string]any{"processes": len(rec.Children),
			"detail": "the release under trial ended: the next start adopts the TV processes"})
		return keepOf(rec)
	}
	if rec != nil && len(rec.Children) > 0 {
		ln.log.Event("launcher_handover_stop", "", map[string]any{"processes": len(rec.Children), "restart": restart})
		stopListed(rec, orphanStopGrace)
	}
	if rec != nil {
		_ = layout.RemoveHandover(dir)
	}
	stopTVJob(dir)
	return nil
}

// keepOf is the process groups a record lists (a process that leads none: its own pid).
func keepOf(rec *layout.Handover) map[int]bool {
	keep := map[int]bool{}
	for _, c := range rec.Children {
		if c.PGID > 0 {
			keep[c.PGID] = true
		} else {
			keep[c.PID] = true
		}
	}
	return keep
}

// nextAdopts reports whether whichever release starts next (current, or previous when
// the guard goes back) has a supervisor that adopts the TV processes.
func (ln *launcher) nextAdopts() bool {
	if ln.cfg.Bundle != "" {
		return false // the desktop apps hand nothing over
	}
	for _, name := range []string{layout.CurrentFile, layout.PreviousFile} {
		v, err := ln.l.ReadPointer(name)
		if err != nil {
			continue
		}
		if layout.ReleaseHandoverProtocol(ln.l.ReleaseDir(v)) < 1 {
			return false
		}
	}
	return true
}
