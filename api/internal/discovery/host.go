package discovery

// The announced host name. zeroconf.Register takes os.Hostname() and appends the
// domain unless the name already ends in it, but it compares against "local." with
// the dot: a Mac's "Studio.local" never matches and the service's target becomes
// "Studio.local.local.", which resolves to nothing. So every announcement goes
// through RegisterProxy with the bare host name and the addresses Register would
// have used. cmd/maktaba-server/internal/mdns has the same helper (another module).

import (
	"errors"
	"net"
	"os"
	"strings"

	"github.com/grandcat/zeroconf"
)

// register announces instance on port with this machine's bare host name, so the
// service's target is "<host>.local.".
func register(instance, service, domain string, port int, txt []string) (*zeroconf.Server, error) {
	h, _ := os.Hostname()
	ips := interfaceIPs()
	if len(ips) == 0 {
		return nil, errors.New("no multicast interface has an address")
	}
	return zeroconf.RegisterProxy(instance, service, domain, port, bareHost(h), ips, txt, nil)
}

// LocalHostName is the name the announcement's target resolves as: this machine's bare
// host name in .local ("Studio.local").
func LocalHostName() string {
	h, _ := os.Hostname()
	return bareHost(h) + ".local"
}

// bareHost is a host name without its domain ("Studio.local." → "Studio"); an empty
// one is "maktaba".
func bareHost(h string) string {
	h = strings.Trim(strings.TrimSpace(h), ".")
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		return "maktaba"
	}
	return h
}

// interfaceIPs are the addresses zeroconf.Register announces: every IPv4 of the up,
// multicast interfaces, and their global IPv6 (link-local when an interface has none).
func interfaceIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		out = append(out, announcedIPs(addrs)...)
	}
	return out
}

// announcedIPs picks one interface's announced addresses.
func announcedIPs(addrs []net.Addr) []string {
	var v4, v6, v6local []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		switch ip := ipnet.IP; {
		case ip.To4() != nil:
			v4 = append(v4, ip.String())
		case ip.IsGlobalUnicast():
			v6 = append(v6, ip.String())
		case ip.IsLinkLocalUnicast():
			v6local = append(v6local, ip.String())
		}
	}
	if len(v6) == 0 {
		v6 = v6local
	}
	return append(v4, v6...)
}
