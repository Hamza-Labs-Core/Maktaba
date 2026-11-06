package supervisor

import (
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Epic 53 story 53.18 §4 (T03): every process component of the catalog is in exactly one
// status group, or in NoStatusGroup with a reason; every id the table names is in the
// catalog; a component's features are in its group's features_down.
func TestStatusMapCoversTheCatalog(t *testing.T) {
	if bad := statusMapProblems(components.Default().Components()); len(bad) > 0 {
		t.Fatal(strings.Join(bad, "\n"))
	}
}

// A process component added without a group fails the check, by name.
func TestStatusMapNamesAComponentWithNoGroup(t *testing.T) {
	list := append(components.Default().Components(),
		&components.Component{ID: "new.thing", Kind: "process", Processes: []string{"newthing/"}})
	bad := statusMapProblems(list)
	if len(bad) != 1 || !strings.Contains(bad[0], "new.thing") {
		t.Fatalf("want one problem naming new.thing, got %v", bad)
	}
}

func statusMapProblems(list []*components.Component) []string {
	byID := map[string]*components.Component{}
	for _, c := range list {
		byID[c.ID] = c
	}
	var bad []string
	seen := map[string]int{}
	for _, g := range StatusGroups {
		for _, id := range g.Components {
			seen[id]++
			c, ok := byID[id]
			if !ok {
				bad = append(bad, "status group "+g.Name+" names "+id+", which the catalog does not have")
				continue
			}
			for _, f := range c.Features {
				if !hasWord(g.FeaturesDown, f) {
					bad = append(bad, id+"'s feature "+f+" is not in group "+g.Name+"'s features_down")
				}
			}
		}
	}
	for _, c := range list {
		if len(c.Processes) == 0 {
			continue
		}
		_, skipped := NoStatusGroup[c.ID]
		switch {
		case seen[c.ID] == 0 && !skipped:
			bad = append(bad, "process component "+c.ID+" is in no status group (statusmap.go): give it one, or a reason in NoStatusGroup")
		case seen[c.ID] > 1:
			bad = append(bad, c.ID+" is in more than one status group")
		case seen[c.ID] > 0 && skipped:
			bad = append(bad, c.ID+" is in a group and in NoStatusGroup")
		}
	}
	for id := range NoStatusGroup {
		if _, ok := byID[id]; !ok {
			bad = append(bad, "NoStatusGroup names "+id+", which the catalog does not have")
		}
	}
	return bad
}
