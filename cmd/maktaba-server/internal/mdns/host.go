package mdns

// The announced host name. zeroconf.Register takes os.Hostname() and appends the
// domain unless the name already ends in it, but it compares against "local." with
// the dot: a Mac's "Studio.local" never matches and the service's target becomes
// "Studio.local.local.", which resolves to nothing. So every announcement goes
// through RegisterProxy with the bare host name and the addresses Register would
// have used. The same file is in api/internal/discovery and
// cmd/maktaba-server/internal/mdns (two modules); tools/ci/mdns_copies.py checks they stay
// the same.

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
// service's target is "<host>.local.": through the system's responder where it has one
// (announce.go), else through zeroconf.
func register(instance, service, domain string, port int, txt []string) (announcement, error) {
	// the system's responder already answers for the host name: it announces the service
	// for us
	r := systemResponder()
	switch r.kind {
	case viaDNSSD:
		return startDNSSD(r.path, instance, service, domain, port, txt)
	case viaAvahi:
		return startAvahi(r.path, instance, service, domain, port, txt)
	case viaDNSAPI:
		return startDNSAPI(systemDNSAPIImpl(), instance, service, domain, announcedHost(r)+".local", port, interfaceIPs(), txt)
	case viaResolved:
		api, err := newResolvedAPI()
		if err != nil {
			return nil, err
		}
		return startResolved(api, instance, service, domain, port, txt)
	}
	ips := interfaceIPs()
	if len(ips) == 0 {
		return nil, errors.New("no multicast interface has an address")
	}
	host := announcedHost(r)
	srv, err := zeroconf.RegisterProxy(zeroconfInstance(dnsLabel(instance, 63)), service, domain, port, host, ips, txt, nil)
	if err != nil {
		return nil, err
	}
	// zeroconf answers nothing asked of the target itself (announce_hostaddr.go); without
	// that answerer it still announces the service, its target's addresses with it
	addrs, _ := startHostAddrs(host+"."+strings.Trim(domain, "."), ips)
	return &zeroconfHost{Server: srv, addrs: addrs}, nil
}

// ownHostSuffix makes the name zeroconf answers for when Avahi or systemd-resolved holds
// the host's own.
const ownHostSuffix = "-maktaba"

// announcedHost is the bare name the announcement's target is: this machine's (as
// systemd-resolved answers for it, where it announces), or, when zeroconf must answer
// beside an Avahi or a resolved it cannot ask, a name of its own beside it.
func announcedHost(r responder) string {
	if r.kind == viaResolved && r.host != "" {
		return r.host
	}
	h := bareHost(hostName())
	if r.kind == viaZeroconfOwnHost {
		return dnsLabel(h, 63-len(ownHostSuffix)) + ownHostSuffix
	}
	return h
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
