package config

import (
	"net"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// lanIP returns this machine's LAN address. A package var so tests can
// pin it.
var lanIP = detectLANIP

// PublicStreamURL is the streaming service's base URL as other devices
// on the network (Plex, a TV, a phone) reach it: [server] public_url
// when set, else http://<host>:<port> from stream_addr, where a wildcard
// or empty host (":8081", "0.0.0.0:8782") becomes the machine's LAN IP.
func (c Config) PublicStreamURL() string {
	if u := strings.TrimRight(strings.TrimSpace(c.Server.PublicURL), "/"); u != "" {
		if !strings.Contains(u, "://") {
			u = "http://" + u
		}
		return u
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(c.Server.StreamAddr))
	if err != nil || port == "" {
		host, port = "", ports.StreamingPublic.PortString()
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = lanIP()
	}
	return "http://" + net.JoinHostPort(host, port)
}

// detectLANIP picks the address the default route leaves from (a UDP
// "dial" sends nothing), falling back to the first private IPv4 on an
// up interface, then loopback.
func detectLANIP() string {
	if conn, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		defer func() { _ = conn.Close() }()
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok && a.IP != nil && !a.IP.IsLoopback() {
			return a.IP.String()
		}
	}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip4 := n.IP.To4(); ip4 != nil && ip4.IsPrivate() {
					return ip4.String()
				}
			}
		}
	}
	return "127.0.0.1"
}

// PublicWebURL is this machine's web address as other devices reach it (Epic 35 story
// 35.11: the Machines page links each machine, the cluster sign-in redirects there):
// [server] web_public_url when set, else http://<host>:<port of web_listen>, the host
// taken from PublicStreamURL (public_url's host, or this machine's LAN IP).
func (c Config) PublicWebURL() string {
	if u := strings.TrimRight(strings.TrimSpace(c.Server.WebPublicURL), "/"); u != "" {
		if !strings.Contains(u, "://") {
			u = "http://" + u
		}
		return u
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(c.Server.WebListen))
	if err != nil || port == "" {
		port = ports.Web.PortString()
	}
	stream := c.PublicStreamURL()
	host := strings.TrimPrefix(strings.TrimPrefix(stream, "http://"), "https://")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), port)
}

// PublicAPIURL is this machine's API as the cluster's other machines reach it (a home's
// address in their node.json): http://<host>:<port of listen>, the host taken as
// PublicWebURL takes it.
func (c Config) PublicAPIURL() string {
	_, port, err := net.SplitHostPort(strings.TrimSpace(c.Server.Listen))
	if err != nil || port == "" {
		port = ports.APIPublic.PortString()
	}
	web := strings.TrimPrefix(strings.TrimPrefix(c.PublicWebURL(), "http://"), "https://")
	host := web
	if h, _, err := net.SplitHostPort(web); err == nil {
		host = h
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), port)
}
