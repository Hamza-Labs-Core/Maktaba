package hdhr

import (
	"context"
	"net"
	"strings"
	"time"
)

// ssdpAddr is the standard UPnP multicast group/port.
const ssdpAddr = "239.255.255.250:1900"

// ssdpST is the search target a real HDHomeRun answers to.
const ssdpST = "urn:schemas-upnp-org:device:MediaServer:1"

// isMSearchFor reports whether a received SSDP datagram is an M-SEARCH
// our device should answer (AC1). We answer ssdp:all and the MediaServer
// device type (what Plex/Jellyfin probe with).
func isMSearchFor(payload string) bool {
	if !strings.HasPrefix(payload, "M-SEARCH") {
		return false
	}
	lower := strings.ToLower(payload)
	if !strings.Contains(lower, "man:") || !strings.Contains(lower, "ssdp:discover") {
		return false
	}
	return strings.Contains(payload, "ssdp:all") ||
		strings.Contains(payload, ssdpST) ||
		strings.Contains(strings.ToLower(payload), "upnp:rootdevice")
}

// buildSSDPResponse builds the unicast reply to an M-SEARCH, pointing at
// this device's /device.xml under the location base (AC1). Pure so the
// header format is unit-tested without a socket.
func buildSSDPResponse(locationBase, deviceUUID string) string {
	return strings.Join([]string{
		"HTTP/1.1 200 OK",
		"CACHE-CONTROL: max-age=1800",
		"EXT:",
		"LOCATION: " + locationBase + "/device.xml",
		"SERVER: Maktaba/1.0 UPnP/1.0 HDHomeRun/1.0",
		"ST: " + ssdpST,
		"USN: uuid:" + deviceUUID + "::" + ssdpST,
		"", "",
	}, "\r\n")
}

// Responder listens for SSDP M-SEARCH datagrams and replies with our
// device location. It is started only when the feature is enabled (AC9);
// when off it is never constructed and the LAN sees nothing.
type Responder struct {
	// LocationBase is the http base advertised in the LOCATION header
	// (scheme://host). DeviceUUID is the persisted UPnP UDN.
	LocationBase string
	DeviceUUID   string
}

// Run blocks listening on udp/1900 until ctx is cancelled, answering each
// matching M-SEARCH unicast. Errors binding the socket are returned (the
// caller logs and continues — SSDP is best-effort over Docker bridges,
// see deploy notes).
func (s *Responder) Run(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", ssdpAddr)
	if err != nil {
		return err
	}
	conn, err := net.ListenMulticastUDP("udp", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		if !isMSearchFor(string(buf[:n])) {
			continue
		}
		reply := buildSSDPResponse(s.LocationBase, s.DeviceUUID)
		_, _ = conn.WriteToUDP([]byte(reply), src)
	}
}
