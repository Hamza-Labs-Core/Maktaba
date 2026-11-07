// Package directaccess is the home's side of direct remote access (Epic 50, story 50.15): it
// asks the router to open a port (UPnP-IGD, NAT-PMP, then PCP), notices a shared address
// (CGNAT), keeps the home's key and certificate for *.<home id>.<its domain>, serves the same
// web and API as the LAN port over TLS on the direct listener, and tells the cloud through the
// relay agent (POST /admin/cloud-link/direct). Its three loops are the cloud.direct
// component's: direct_listener, direct_portmap and direct_cert.
package directaccess

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/netip"
	"strconv"
	"time"
)

// Mapping is a port the router opened to this home.
type Mapping struct {
	By         string // upnp, natpmp, pcp
	Internal   int
	External   int
	ExternalIP netip.Addr // the router's external address, when it said
	Lifetime   time.Duration
}

// Mapper is one way of asking the router.
type Mapper interface {
	Name() string
	// Map opens external (0: the router picks) to internal on this machine for lifetime.
	Map(ctx context.Context, internal, external int, lifetime time.Duration) (Mapping, error)
	// Unmap closes it.
	Unmap(ctx context.Context, m Mapping) error
}

// ErrNoGateway: no router answered.
var ErrNoGateway = errors.New("directaccess: no router answered (UPnP, NAT-PMP, PCP)")

// Lease is how long a mapping is asked for; it is renewed every RenewEvery.
const (
	Lease      = time.Hour
	RenewEvery = 20 * time.Minute
)

// MapFirst asks each mapper in order (UPnP-IGD, NAT-PMP, PCP) and answers the first mapping
// made, asking for want first (the port the home had) and the router's choice after.
func MapFirst(ctx context.Context, mappers []Mapper, internal, want int) (Mapping, Mapper, error) {
	var errs []error
	for _, m := range mappers {
		got, err := m.Map(ctx, internal, want, Lease)
		if err == nil {
			return got, m, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return Mapping{}, nil, ErrNoGateway
	}
	return Mapping{}, nil, errors.Join(append([]error{ErrNoGateway}, errs...)...)
}

// Shared says whether ip can't be reached from outside: carrier-grade NAT (100.64.0.0/10), a
// private, loopback or link-local address. A router reporting one as its external address
// means the provider shares it, so direct access can't work (the cloud's direct.Shared).
func Shared(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

// randomPorts are n ports in 30000–49999, the range a router's choice is asked in.
func randomPorts(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 30000 + rand.IntN(20000)
	}
	return out
}

// ParsePort reads a TCP port, 1–65535.
func ParsePort(s string) (int, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("directaccess: port %q is not in 1–65535", s)
	}
	return int(n), nil
}

// portValue is p on the wire: 1–65535, or 0 where the router may choose (anyOK).
func portValue(p int, anyOK bool) (uint16, error) {
	if p < 0 || p > math.MaxUint16 || (p == 0 && !anyOK) {
		return 0, fmt.Errorf("directaccess: port %d is not in 1–65535", p)
	}
	return uint16(p), nil
}

// leaseSeconds is d in whole seconds for the wire, 0 for a negative d and at most what 32 bits
// hold.
func leaseSeconds(d time.Duration) uint32 {
	s := int64(d / time.Second)
	switch {
	case s <= 0:
		return 0
	case s > math.MaxUint32:
		return math.MaxUint32
	}
	return uint32(s)
}
