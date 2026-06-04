// Package mdns advertises the running server over multicast DNS so
// client apps (mobile/desktop) can discover it on the LAN without the
// user typing an IP. It registers the `_maktaba._tcp` service type with
// the public API port and a small TXT record carrying the version and
// API path prefix.
package mdns

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

// Advertiser owns a registered mDNS service; Close withdraws it.
type Advertiser struct {
	server *zeroconf.Server
}

// Advertise registers `_maktaba._tcp` on the given public API port.
// instance is the human-visible name shown in discovery UIs (defaults to
// the hostname). version is embedded in the TXT record. A nil return
// with a nil error never happens; on failure the error is returned and
// the caller can log-and-continue — discovery is a convenience, not a
// hard dependency.
func Advertise(instance string, port int, version string) (*Advertiser, error) {
	if instance == "" {
		if h, err := os.Hostname(); err == nil {
			instance = h
		} else {
			instance = "maktaba"
		}
	}

	txt := []string{
		"version=" + version,
		"api=/api",
		"path=/",
	}

	server, err := zeroconf.Register(
		instance,        // instance name
		"_maktaba._tcp", // service type
		"local.",        // domain
		port,            // service port
		txt,             // TXT records
		nil,             // advertise on all interfaces
	)
	if err != nil {
		return nil, fmt.Errorf("register mDNS service: %w", err)
	}
	return &Advertiser{server: server}, nil
}

// Close withdraws the mDNS advertisement.
func (a *Advertiser) Close() {
	if a != nil && a.server != nil {
		a.server.Shutdown()
	}
}

// PortFromListen extracts the numeric port from a bind spec such as
// "0.0.0.0:8080" or ":8080". Returns the fallback when unparseable.
func PortFromListen(listen string, fallback int) int {
	i := strings.LastIndexByte(listen, ':')
	if i < 0 || i+1 >= len(listen) {
		return fallback
	}
	p, err := strconv.Atoi(listen[i+1:])
	if err != nil || p <= 0 || p > 65535 {
		return fallback
	}
	return p
}
