package hostinfo

// The container's network (Epic 59 story 59.08). On the host's network (the default of
// every compose file and template) apps find the home by mDNS and TVs by SSDP. On a
// Docker bridge they cannot: multicast does not leave the bridge, so Maktaba turns both
// off and setup says how apps still connect (its address, its HTTPS name, a cloud code).
// A bridge is told by the default route's interface: its driver is veth, the container's
// end of the bridge's link. macvlan and ipvlan (Unraid's br0 custom network) put the
// container on the LAN itself, where multicast works: they count as the host's.

import (
	"strconv"
	"strings"
)

// EnvNetwork forces the reading: "host" or "bridge" (a network the detection reads
// wrong).
const EnvNetwork = "MAKTABA_NETWORK"

// The network modes.
const (
	NetworkHost    = "host"
	NetworkBridge  = "bridge"
	NetworkMacvlan = "macvlan"
)

// Network is the container's network; Mode is "" outside a container.
type Network struct {
	Mode string `json:"mode,omitempty"`
	// From is "env" (MAKTABA_NETWORK) or "detected".
	From string `json:"from,omitempty"`
	// Interface is the default route's, Driver its driver (veth, macvlan, e1000e, bridge…).
	Interface string `json:"interface,omitempty"`
	Driver    string `json:"driver,omitempty"`
}

// Bridge is whether apps cannot find this machine by multicast.
func (n Network) Bridge() bool { return n.Mode == NetworkBridge }

// InContainer is whether p is a container: the image says so (MAKTABA_CONTAINER), or
// Docker's or Podman's marker file is there.
func InContainer(p Probe) bool {
	if p.Env == nil {
		p.Env = func(string) string { return "" }
	}
	return strings.TrimSpace(p.Env("MAKTABA_CONTAINER")) != "" || p.exists("/.dockerenv") || p.exists("/run/.containerenv")
}

func readNetwork(p Probe) Network {
	switch v := strings.ToLower(strings.TrimSpace(p.Env(EnvNetwork))); v {
	case NetworkHost, NetworkBridge, NetworkMacvlan:
		return Network{Mode: v, From: "env"}
	}
	if !InContainer(p) {
		return Network{}
	}
	n := Network{Mode: NetworkHost, From: "detected"}
	route, _ := p.read("/proc/net/route")
	n.Interface = defaultRouteInterface(route)
	if n.Interface == "" || p.Driver == nil {
		return n
	}
	n.Driver = p.Driver(n.Interface)
	switch n.Driver {
	case "veth":
		n.Mode = NetworkBridge
	case "macvlan", "ipvlan":
		n.Mode = NetworkMacvlan
	}
	return n
}

// defaultRouteInterface is the interface of /proc/net/route's default route (destination
// and mask 0), "" when there is none.
func defaultRouteInterface(table string) string {
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[0] == "Iface" {
			continue
		}
		dest, err1 := strconv.ParseUint(f[1], 16, 32)
		mask, err2 := strconv.ParseUint(f[7], 16, 32)
		if err1 == nil && err2 == nil && dest == 0 && mask == 0 {
			return f[0]
		}
	}
	return ""
}
