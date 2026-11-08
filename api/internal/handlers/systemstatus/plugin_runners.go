package systemstatus

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

// Epic 54: the Plugin runners line of System health. A first-party runner that failed (it
// kept exiting, or its sandbox cannot start) leaves the stages set to run as plugins in
// it running in the worker, outside their sandbox: no job is lost and no viewer feature
// stops, so nothing else says it. The supervisor of each machine names its failed runners
// in the component's status (detail failed_runners); job_stages says which stages are set
// to plugin. A runner that is held, idle or restarting is not counted.

// RunnerIssue is one failed runner on one machine and its stages set to plugin.
type RunnerIssue struct {
	Machine string   `json:"machine"`
	Runner  string   `json:"runner"`
	Stages  []string `json:"stages"`
}

// pluginStages answers the stages job_stages sets to run as plugins.
func (h *Handler) pluginStages(ctx context.Context) ([]string, error) {
	if h.PluginStages != nil {
		return h.PluginStages(ctx)
	}
	if h.DB == nil {
		return nil, nil
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT stage FROM job_stages WHERE impl = 'plugin' ORDER BY stage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// runnerIssues are the failed runners in rows, with the stages of plugin (set to run as
// plugins) whose runner group each one is: home first, then by machine and runner.
func runnerIssues(rows []store.Status, plugin []string, groups map[string]string) []RunnerIssue {
	seen := map[[2]string]bool{}
	var out []RunnerIssue
	for _, r := range rows {
		for _, in := range stringList(r.Detail["failed_runners"]) {
			k := [2]string{r.Machine, in}
			if seen[k] {
				continue
			}
			seen[k] = true
			is := RunnerIssue{Machine: r.Machine, Runner: in, Stages: []string{}}
			g := sharedplugins.RunnerGroupOf(in)
			for _, st := range plugin {
				if groups[st] == g {
					is.Stages = append(is.Stages, st)
				}
			}
			out = append(out, is)
		}
	}
	slices.SortFunc(out, func(a, b RunnerIssue) int {
		if a.Machine != b.Machine {
			if a.Machine == store.Home {
				return -1
			}
			if b.Machine == store.Home {
				return 1
			}
			return strings.Compare(a.Machine, b.Machine)
		}
		return strings.Compare(a.Runner, b.Runner)
	})
	return out
}

// stringList reads a detail list: []string as the supervisor writes it, []any once it went
// through JSON.
func stringList(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func checkPluginRunners(ctx context.Context, e *env) []Item {
	if e.h.Components == nil || len(sharedplugins.FirstPartyGroups()) == 0 {
		return nil
	}
	it := base("plugin_runners", GroupServices, KindPluginRunners, "Plugin runners")
	it.Link = "/settings/plugins"
	rows, err := e.h.Components.Statuses(ctx)
	if err != nil {
		it.set(StatusDegraded, "plugin_runners_unreadable", map[string]any{"detail": errText(err)},
			"The plugin runners' health cannot be read: "+errText(err))
		return one(it)
	}
	// unreadable stages: the failed runners are still named, with no stage
	plugin, _ := e.h.pluginStages(ctx)
	issues := runnerIssues(rows, plugin, sharedplugins.FirstPartyStageGroups())
	it.Details = map[string]any{"failed": nonNilRunnerIssues(issues)}
	if len(issues) == 0 {
		it.set(StatusOK, "plugin_runners_ok", nil, "Every plugin runner with work runs.")
		return one(it)
	}
	first := issues[0]
	it.Attention = true
	it.set(StatusDegraded, "plugin_runners_failed",
		map[string]any{"failed": len(issues), "runner": first.Runner, "machine": first.Machine, "stages": len(first.Stages)},
		fmt.Sprintf("%d failed: the %s runner on %s. Its stages set to run as plugins (%d) run in the worker, "+
			"outside their sandbox, until it runs again.", len(issues), first.Runner, first.Machine, len(first.Stages)))
	return one(it)
}

func nonNilRunnerIssues(s []RunnerIssue) []RunnerIssue {
	if s == nil {
		return []RunnerIssue{}
	}
	return s
}
