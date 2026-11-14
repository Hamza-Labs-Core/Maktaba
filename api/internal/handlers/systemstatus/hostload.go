package systemstatus

// Two things the queues' own health does not show: the machine this server runs on has
// more work than cores (hostload: every job and the database are slow, often because of
// a program outside Maktaba), and workers that are up but do not run their jobs
// (queues.WorkersState). Both are detected here and explained; the workers are restarted
// from the Queues page.

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/hostload"
)

// WorkersHealth is the queues' answer on its workers (queues.Handler); a QueueHealth
// that has it adds the stalled workers to the Queues item.
type WorkersHealth interface {
	WorkersNow(ctx context.Context) (queues.WorkersState, error)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// topText names the programs: "runaway (795%), Some App (398%)".
func topText(top []hostload.Proc) string {
	parts := make([]string, 0, len(top))
	for _, p := range top {
		parts = append(parts, fmt.Sprintf("%s (%d%%)", p.Name, int(math.Round(p.CPUPct))))
	}
	return strings.Join(parts, ", ")
}

// checkHostLoad is the "This machine's load" item: degraded while the machine is
// overloaded, with the programs that use the most CPU. No item where the load cannot be
// read (Windows).
func checkHostLoad(ctx context.Context, e *env) []Item {
	if e.h.HostLoad == nil {
		return nil
	}
	st, ok := e.h.HostLoad(ctx)
	if !ok {
		return nil
	}
	it := base("host_load", GroupServices, KindHostLoad, "This machine's load")
	p := map[string]any{"load1": round1(st.Load1), "load5": round1(st.Load5), "cores": st.Cores,
		"bound": round1(st.Bound), "top": topText(st.Top)}
	it.Details = map[string]any{"load1": st.Load1, "load5": st.Load5, "load15": st.Load15,
		"cores": st.Cores, "bound": st.Bound, "top": st.Top}
	switch {
	case st.Overloaded && len(st.Top) > 0:
		it.set(StatusDegraded, "host_overloaded_top", p, fmt.Sprintf(
			"This machine is overloaded: load %.1f on %d cores. Jobs and the database are slow. Using the most CPU: %s.",
			st.Load1, st.Cores, p["top"]))
	case st.Overloaded:
		it.set(StatusDegraded, "host_overloaded", p, fmt.Sprintf(
			"This machine is overloaded: load %.1f on %d cores. Jobs and the database are slow.", st.Load1, st.Cores))
	default:
		it.set(StatusOK, "host_load_ok", p, fmt.Sprintf("Load %.1f on %d cores.", st.Load1, st.Cores))
	}
	return one(it)
}

// workerKindsText names the stalled workers' kinds once each: "main, transcription".
func workerKindsText(ws []queues.StalledWorker) string {
	var kinds []string
	seen := map[string]bool{}
	for _, w := range ws {
		if !seen[w.Kind] {
			seen[w.Kind] = true
			kinds = append(kinds, w.Kind)
		}
	}
	return strings.Join(kinds, ", ")
}

// stalledWorkersItem turns the Queues item into "workers are not running jobs" when the
// queues say so (false: they run, or the queues cannot say).
func stalledWorkersItem(ctx context.Context, e *env, it *Item) bool {
	wh, ok := e.h.Queues.(WorkersHealth)
	if !ok {
		return false
	}
	ws, err := wh.WorkersNow(ctx)
	if err != nil || ws.State != queues.WorkersNotResponding {
		return false
	}
	p := map[string]any{"workers": len(ws.Stalled), "waiting": ws.Waiting, "kinds": workerKindsText(ws.Stalled)}
	it.Link = "/queue"
	it.Details = map[string]any{"stalled": ws.Stalled, "waiting": ws.Waiting}
	if len(ws.Stalled) == 0 {
		it.set(StatusDegraded, "queues_no_workers", p,
			fmt.Sprintf("%d jobs wait and no worker is running.", ws.Waiting))
		return true
	}
	it.set(StatusDegraded, "queues_workers_stalled", p, fmt.Sprintf(
		"%d workers are not running their jobs (%s); %d jobs wait. Restart them on the Queues page.",
		len(ws.Stalled), p["kinds"], ws.Waiting))
	return true
}
