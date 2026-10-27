package discovery

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/grandcat/zeroconf"
)

// ZeroconfPublisher is the production Publisher (Epic 31 design §2): it
// registers the service with multicast DNS-SD through grandcat/zeroconf.
// Publishing again with the same instance name and port only replaces
// the TXT records (zeroconf re-announces them); any other change
// re-registers.
type ZeroconfPublisher struct {
	mu     sync.Mutex
	server *zeroconf.Server
	last   Service
}

var _ Publisher = (*ZeroconfPublisher)(nil)

// NewZeroconfPublisher returns a publisher that has not announced anything.
func NewZeroconfPublisher() *ZeroconfPublisher {
	return &ZeroconfPublisher{}
}

// TXTRecords renders the TXT map as sorted key=value strings.
func TXTRecords(txt map[string]string) []string {
	out := make([]string, 0, len(txt))
	for k, v := range txt {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// Publish implements Publisher.
func (z *ZeroconfPublisher) Publish(_ context.Context, svc Service) error {
	if svc.Instance == "" || svc.Port <= 0 {
		return errors.New("discovery: instance and port are required")
	}
	if svc.Domain == "" {
		svc.Domain = "local."
	}
	txt := TXTRecords(svc.TXT)
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.server != nil && z.last.Instance == svc.Instance && z.last.Port == svc.Port && z.last.Domain == svc.Domain {
		z.server.SetText(txt)
		z.last = svc
		return nil
	}
	if z.server != nil {
		z.server.Shutdown()
		z.server = nil
	}
	srv, err := register(svc.Instance, ServiceType, svc.Domain, svc.Port, txt)
	if err != nil {
		return err
	}
	z.server, z.last = srv, svc
	return nil
}

// Close implements Publisher.
func (z *ZeroconfPublisher) Close() error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.server != nil {
		z.server.Shutdown()
		z.server = nil
	}
	return nil
}
