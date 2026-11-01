package mdns

// The announced host name. zeroconf.Register takes os.Hostname() and appends the
// domain unless the name already ends in it, but it compares against "local." with
// the dot: a Mac's "Studio.local" never matches and the service's target becomes
// "Studio.local.local.", which resolves to nothing. So every announcement goes
// through RegisterProxy with the bare host name and the addresses Register would
// have used. api/internal/discovery has the same helper (another module).

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// register announces instance on port with this machine's bare host name, so the
// service's target is "<host>.local.".
func register(instance, service, domain string, port int, txt []string) (*zeroconf.Server, error) {
	h := hostName()
	ips := interfaceIPs()
	if len(ips) == 0 {
		return nil, errors.New("no multicast interface has an address")
	}
	return zeroconf.RegisterProxy(instance, service, domain, port, bareHost(h), ips, txt, nil)
}

// hostName is the name this machine answers to in .local: on macOS the Bonjour
// LocalHostName (`scutil --get LocalHostName`, which mDNSResponder answers for), since
// os.Hostname there follows the network (a DHCP or reverse-DNS name such as
// "MahmoudacStudio") and an announcement naming it pointed the apps at a host that
// resolves to nothing; elsewhere os.Hostname.
func hostName() string {
	if runtime.GOOS == "darwin" {
		if n := macLocalHostName(); n != "" {
			return n
		}
	}
	h, _ := os.Hostname()
	return h
}

// macLocalHostName is `scutil --get LocalHostName` ("" when it cannot be read).
var macLocalHostName = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--get", "LocalHostName").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
