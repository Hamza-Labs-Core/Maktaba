package hdhr

import (
	"net"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// Advertiser decides the http base (scheme://host:port) a LAN client is told
// to use in SSDP LOCATION headers and HDHomeRun discover replies. It is the
// base /discover.json reports when a client reaches us on that address:
// MAKTABA_PUBLIC_BASE_URL when set, else http://<our address on the client's
// network>:<the public HTTP port>, so a multi-homed host advertises the
// address the asking client can reach.
type Advertiser struct {
	// PublicBase is MAKTABA_PUBLIC_BASE_URL; it wins when set.
	PublicBase string
	// HTTPAddr is the public listen address (MAKTABA_HTTP_ADDR, ":8081" when
	// empty). A specific host in it is advertised as is.
	HTTPAddr string
	// Addrs lists this host's interface addresses (nil: net.InterfaceAddrs).
	Addrs func() ([]net.Addr, error)
	// Route returns the local address the OS would use to reach remote, for a
	// client on no local subnet (nil: a UDP connect, which sends nothing).
	Route func(remote net.IP) net.IP
}

// DefaultHTTPPort is the streaming service's public port (main.go).
var DefaultHTTPPort = ports.StreamingPublic.PortString()

// port is the public HTTP port, and the listen host when it names one.
func (a Advertiser) hostPort() (host, port string) {
	addr := strings.TrimSpace(a.HTTPAddr)
	if addr == "" {
		return "", DefaultHTTPPort
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", DefaultHTTPPort
	}
	if p == "" || p == "0" {
		p = DefaultHTTPPort
	}
	if ip := net.ParseIP(h); h == "" || (ip != nil && ip.IsUnspecified()) {
		h = ""
	}
	return h, p
}

// BaseFor is the base to advertise to a client at remote.
func (a Advertiser) BaseFor(remote net.IP) string {
	if b := strings.TrimRight(strings.TrimSpace(a.PublicBase), "/"); b != "" {
		return b
	}
	host, port := a.hostPort()
	if host == "" {
		if ip := a.localFor(remote); ip != nil {
			host = ip.String()
		} else {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}

// BaseForLocal is the base a client reaching us on local (one of our
// addresses) should use.
func (a Advertiser) BaseForLocal(local net.IP) string {
	if b := strings.TrimRight(strings.TrimSpace(a.PublicBase), "/"); b != "" {
		return b
	}
	host, port := a.hostPort()
	if host == "" {
		host = local.String()
	}
	return "http://" + net.JoinHostPort(host, port)
}

// localFor picks our IPv4 address on remote's subnet, else the routed one,
// else the first private LAN address.
func (a Advertiser) localFor(remote net.IP) net.IP {
	addrs := a.addrs()
	if remote != nil {
		for _, ad := range addrs {
			if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil && n.Contains(remote) {
				return n.IP.To4()
			}
		}
		route := a.Route
		if route == nil {
			route = routeLocal
		}
		if ip := route(remote); ip != nil && !ip.IsUnspecified() {
			return ip
		}
	}
	for _, ad := range addrs {
		if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.To4()
		}
	}
	return nil
}

func (a Advertiser) addrs() []net.Addr {
	f := a.Addrs
	if f == nil {
		f = net.InterfaceAddrs
	}
	addrs, _ := f()
	return addrs
}

// routeLocal asks the OS which local address routes to remote: connecting a
// UDP socket picks the source address without sending a packet.
func routeLocal(remote net.IP) net.IP {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: remote, Port: HDHRDiscoverPort})
	if err != nil {
		return nil
	}
	defer c.Close()
	if la, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return la.IP
	}
	return nil
}

// remoteIP is the IP of a datagram's source.
func remoteIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.IP
	case nil:
		return nil
	}
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return nil
	}
	return net.ParseIP(h)
}
