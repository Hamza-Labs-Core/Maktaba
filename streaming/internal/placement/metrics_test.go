package placement

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// Story 32.11 TC1: the host gauges and the placement counter.
func TestCollectorAndPlacementCounter(t *testing.T) {
	h := newHarness(homeHost(4), nodeHost("pc", 3))
	before := tvmetrics.Placements.Get(ReasonFirstRequest)
	if _, err := h.svc.Place(context.Background(), uuid.New(), ReasonFirstRequest, ""); err != nil {
		t.Fatal(err)
	}
	if tvmetrics.Placements.Get(ReasonFirstRequest) != before+1 {
		t.Error("placement not counted")
	}
	h.setBeat("pc", time.Minute) // pc stops beating
	h.hosts.Refresh(context.Background())
	fams := h.svc.Collector(func(context.Context) (map[[2]string]int, error) {
		return map[[2]string]int{{"home", "videotoolbox"}: 1}, nil
	})()
	got := map[string]map[string]float64{}
	for _, f := range fams {
		got[f.Name] = map[string]float64{}
		for _, s := range f.Samples {
			got[f.Name][s.Labels[0]] = s.Value
		}
	}
	if got[tvmetrics.HostUp]["home"] != 1 || got[tvmetrics.HostUp]["pc"] != 0 {
		t.Errorf("host_up %v", got[tvmetrics.HostUp])
	}
	if got[tvmetrics.HostCapacity]["home"] != 4 || got[tvmetrics.HostUsed]["home"] != 1 {
		t.Errorf("capacity %v used %v", got[tvmetrics.HostCapacity], got[tvmetrics.HostUsed])
	}
	if got[tvmetrics.ChannelsOnAir]["home"] != 1 {
		t.Errorf("on air %v", got[tvmetrics.ChannelsOnAir])
	}
}
