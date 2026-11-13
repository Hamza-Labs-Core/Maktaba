package config

import (
	"net"
	"os"
	"strconv"
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
	if c.Server.PublicStreamPort > 0 {
		port = strconv.Itoa(c.Server.PublicStreamPort)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = lanIP()
	}
	return "http://" + net.JoinHostPort(host, port)
}

// EnvHostIP is the address the household reaches this machine at, set by a container's
// compose file or template (Epic 59 story 59.08): on a bridge network the container's own
// address is the bridge's, which no device reaches.
const EnvHostIP = "MAKTABA_HOST_IP"

// detectLANIP is MAKTABA_HOST_IP when it holds an address, else the address the default
// route leaves from (a UDP "dial" sends nothing), falling back to the first private IPv4
// on an up interface, then loopback.
func detectLANIP() string {
	if ip := net.ParseIP(strings.TrimSpace(os.Getenv(EnvHostIP))); ip != nil {
		return ip.String()
	}
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
	port := strconv.Itoa(c.PublicWebPort())
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
	port := strconv.Itoa(c.PublicAPIPort())
	web := strings.TrimPrefix(strings.TrimPrefix(c.PublicWebURL(), "http://"), "https://")
	host := web
	if h, _, err := net.SplitHostPort(web); err == nil {
		host = h
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), port)
}

// PublicWebPort is the port the household's devices reach the web app at: [server]
// public_web_port when set (a bridge network's published port), else web_listen's.
func (c Config) PublicWebPort() int {
	if c.Server.PublicWebPort > 0 {
		return c.Server.PublicWebPort
	}
	return listenPort(c.Server.WebListen, ports.Web.Port)
}

// PublicAPIPort is the port the household's devices and machines reach the API at: [server]
// public_api_port when set, else listen's.
func (c Config) PublicAPIPort() int {
	if c.Server.PublicAPIPort > 0 {
		return c.Server.PublicAPIPort
	}
	return listenPort(c.Server.Listen, ports.APIPublic.Port)
}

// PublicStreamPort is the port the household's devices reach live TV at: [server]
// public_stream_port when set, else stream_addr's.
func (c Config) PublicStreamPort() int {
	if c.Server.PublicStreamPort > 0 {
		return c.Server.PublicStreamPort
	}
	return listenPort(c.Server.StreamAddr, ports.StreamingPublic.Port)
}

// listenPort is bind addr's port, def when it names none.
func listenPort(addr string, def int) int {
	_, p, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 || n > 65535 {
		return def
	}
	return n
}
