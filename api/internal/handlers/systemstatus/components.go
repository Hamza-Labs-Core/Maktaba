package systemstatus

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Epic 51 story 51.14: the Components line of System health. Only a component that
// failed, or stayed degraded longer than componentDegradedAfter, needs a look. Off,
// stopped and waiting are what an admin chose (or follow from it) and never count; a
// component waiting on a requirement that failed is not counted either: the
// requirement's own failure is the item.

// ComponentStatuses reads every machine's component_status rows (*store.Store).
type ComponentStatuses interface {
	Statuses(ctx context.Context) ([]store.Status, error)
}

// componentDegradedAfter is how long a component may stay degraded before it counts.
const componentDegradedAfter = 10 * time.Minute

// ComponentIssue is one component that needs a look, on one machine.
type ComponentIssue struct {
	Machine string    `json:"machine"`
	ID      string    `json:"id"`
	Phase   string    `json:"phase"`
	Reason  string    `json:"reason,omitempty"`
	Since   time.Time `json:"since"`
}

// componentIssues are the failed components, then the ones degraded too long: home
// first, then by machine and id.
func componentIssues(rows []store.Status, now time.Time) (failed, degraded []ComponentIssue) {
	for k, s := range store.RollupAll(rows) {
		is := ComponentIssue{Machine: k.Machine, ID: k.ID, Phase: s.Phase, Reason: s.Reason(), Since: s.Since}
		switch s.Phase {
		case components.PhaseFailed:
			failed = append(failed, is)
		case components.PhaseDegraded:
			if !s.Since.IsZero() && now.Sub(s.Since) > componentDegradedAfter {
				degraded = append(degraded, is)
			}
		}
	}
	order := func(a, b ComponentIssue) int {
		if a.Machine != b.Machine {
			if a.Machine == store.Home {
				return -1
			}
			if b.Machine == store.Home {
				return 1
			}
			return strings.Compare(a.Machine, b.Machine)
		}
		return strings.Compare(a.ID, b.ID)
	}
	slices.SortFunc(failed, order)
	slices.SortFunc(degraded, order)
	return failed, degraded
}

func checkComponents(ctx context.Context, e *env) []Item {
	if e.h.Components == nil {
		return nil
	}
	it := base("components", GroupServices, KindComponents, "Components")
	it.Link = "/settings/components"
	rows, err := e.h.Components.Statuses(ctx)
	if err != nil {
		it.set(StatusDegraded, "components_unreadable", map[string]any{"detail": errText(err)},
			"The components' health cannot be read: "+errText(err))
		return one(it)
	}
	failed, degraded := componentIssues(rows, e.h.now())
	it.Details = map[string]any{"failed": nonNilIssues(failed), "degraded": nonNilIssues(degraded)}
	words := func(is ComponentIssue) string {
		if is.Reason == "" {
			return is.ID
		}
		return fmt.Sprintf("%s (%s)", is.ID, strings.ReplaceAll(is.Reason, "_", " "))
	}
	switch {
	case len(failed) > 0:
		first := failed[0]
		it.Attention = true
		it.set(StatusDegraded, "components_failed",
			map[string]any{"failed": len(failed), "component": first.ID, "why": first.Reason, "machine": first.Machine},
			fmt.Sprintf("%d failed: %s.", len(failed), words(first)))
	case len(degraded) > 0:
		first := degraded[0]
		it.Attention = true
		it.set(StatusDegraded, "components_degraded",
			map[string]any{"degraded": len(degraded), "component": first.ID, "why": first.Reason, "machine": first.Machine},
			fmt.Sprintf("%d degraded: %s.", len(degraded), words(first)))
	default:
		it.set(StatusOK, "components_ok", nil, "All parts running.")
	}
	return one(it)
}

func nonNilIssues(s []ComponentIssue) []ComponentIssue {
	if s == nil {
		return []ComponentIssue{}
	}
	return s
}
