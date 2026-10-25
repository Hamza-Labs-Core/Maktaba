package main

import (
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The TV handover across a supervisor restart (Epic 35 story 35.7, design §8.1). The
// server keeps <updates>/handover.json, the record of the TV processes that may outlive
// it (layout.Handover). When it exits 75 having handed them over (the record says so,
// written by that server on this boot) they run on, and the next server adopts them:
// the launcher leaves them alone. On any other exit, and on a stop, it stops them and
// removes the record (the server stops them itself on a stop; this is for one that
// could not: killed after the grace, crashed).

// survivors deals with the record once the server (pid) has exited; restart: it exited
// 75. It returns the process groups the launcher keeps of what the server left behind
// (Linux: they are its orphans, which it otherwise stops), nil when it keeps none.
func (ln *launcher) survivors(pid int, restart bool) map[int]bool {
	dir := ln.l.UpdatesDir()
	rec, err := layout.ReadHandover(dir)
	if err != nil {
		ln.log.Event("launcher_handover_unreadable", "", map[string]any{"err": err.Error()})
		_ = layout.RemoveHandover(dir)
		stopTVJob(dir)
		return nil
	}
	if restart && rec.HandedOverBy(pid) {
		keep := map[int]bool{}
		for _, c := range rec.Children {
			if c.PGID > 0 {
				keep[c.PGID] = true
			} else {
				keep[c.PID] = true
			}
		}
		ln.log.Event("launcher_handover", "", map[string]any{"processes": len(rec.Children)})
		return keep
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
