package placement

import (
	"context"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// OnAirCounter counts the channels on air per host and encoder (the
// channel_runtime rows with a fresh heartbeat).
type OnAirCounter func(ctx context.Context) (map[[2]string]int, error)

// Collector is the scrape-time collector of the hosts (story 32.11):
// maktaba_tv_host_up{host,role}, maktaba_tv_host_capacity{host},
// maktaba_tv_host_used{host} (active placements) and
// maktaba_tv_channels_on_air{host,encoder}. onAir may be nil.
func (s *Service) Collector(onAir OnAirCounter) tvmetrics.Collector {
	return func() []tvmetrics.GaugeFamily {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		up := tvmetrics.GaugeFamily{Name: tvmetrics.HostUp, Help: "Streaming host up (1) or down (0).",
			LabelNames: []string{"host", "role"}}
		capacity := tvmetrics.GaugeFamily{Name: tvmetrics.HostCapacity, Help: "Channels a host may carry at once.",
			LabelNames: []string{"host"}}
		used := tvmetrics.GaugeFamily{Name: tvmetrics.HostUsed, Help: "Channels placed on a host.",
			LabelNames: []string{"host"}}
		perHost := map[string]int{}
		if ps, err := s.Store.All(ctx); err == nil {
			for _, p := range ps {
				if p.Active() {
					perHost[p.Host]++
				}
			}
		}
		for _, h := range s.Hosts.Snapshot() {
			v := 0.0
			if s.Hosts.Alive(ctx, h.ID) {
				v = 1
			}
			up.Samples = append(up.Samples, tvmetrics.Gauge{Labels: []string{h.ID, h.Role}, Value: v})
			capacity.Samples = append(capacity.Samples, tvmetrics.Gauge{Labels: []string{h.ID}, Value: float64(h.Capacity)})
			used.Samples = append(used.Samples, tvmetrics.Gauge{Labels: []string{h.ID}, Value: float64(perHost[h.ID])})
		}
		out := []tvmetrics.GaugeFamily{up, capacity, used}
		if onAir != nil {
			air := tvmetrics.GaugeFamily{Name: tvmetrics.ChannelsOnAir, Help: "Channels on air per host and encoder.",
				LabelNames: []string{"host", "encoder"}}
			if m, err := onAir(ctx); err == nil {
				for k, n := range m {
					air.Samples = append(air.Samples, tvmetrics.Gauge{Labels: []string{k[0], k[1]}, Value: float64(n)})
				}
			}
			out = append(out, air)
		}
		return out
	}
}
