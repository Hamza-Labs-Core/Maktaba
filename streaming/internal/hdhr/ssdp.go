package hdhr

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
)

// ssdpAddr is the standard UPnP multicast group/port.
const ssdpAddr = "239.255.255.250:1900"

// ssdpST is the device type a real HDHomeRun announces.
const ssdpST = "urn:schemas-upnp-org:device:MediaServer:1"

// ssdpRootDevice is the UPnP root-device search target.
const ssdpRootDevice = "upnp:rootdevice"

// DefaultNotifyEvery is how often ssdp:alive is re-announced (well inside the
// 1800 s max-age).
const DefaultNotifyEvery = 5 * time.Minute

const ssdpServer = "Maktaba/1.0 UPnP/1.0 HDHomeRun/1.0"

// ssdpHeader returns a header's value from an SSDP datagram (case-insensitive
// name), or "".
func ssdpHeader(payload, name string) string {
	for _, line := range strings.Split(payload, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// isMSearchFor reports whether a received SSDP datagram is an M-SEARCH
// our device should answer (AC1). We answer ssdp:all, upnp:rootdevice and the
// MediaServer device type (what Plex/Jellyfin probe with).
func isMSearchFor(payload string) bool {
	return len(msearchTargets(payload, "")) > 0
}

// msearchTargets is the ST values to answer an M-SEARCH with: one reply per
// target (ssdp:all gets all three: root device, our uuid, the device type).
func msearchTargets(payload, deviceUUID string) []string {
	if !strings.HasPrefix(payload, "M-SEARCH") {
		return nil
	}
	if !strings.Contains(strings.ToLower(ssdpHeader(payload, "MAN")), "ssdp:discover") {
		return nil
	}
	st := ssdpHeader(payload, "ST")
	switch {
	case st == "ssdp:all":
		out := []string{ssdpRootDevice}
		if deviceUUID != "" {
			out = append(out, "uuid:"+deviceUUID)
		}
		return append(out, ssdpST)
	case strings.EqualFold(st, ssdpRootDevice):
		return []string{ssdpRootDevice}
	case st == ssdpST:
		return []string{ssdpST}
	case deviceUUID != "" && strings.EqualFold(st, "uuid:"+deviceUUID):
		return []string{st}
	}
	return nil
}

// ssdpUSN is the USN for a target: uuid:<UDN> alone for the uuid target,
// uuid:<UDN>::<target> otherwise (UPnP Device Architecture 1.1, 1.1.2). The
// uuid is the persisted hdhr_device.device_uuid, the same as device.xml's
// UDN, so it is stable across restarts.
func ssdpUSN(deviceUUID, target string) string {
	if strings.HasPrefix(target, "uuid:") {
		return "uuid:" + deviceUUID
	}
	return "uuid:" + deviceUUID + "::" + target
}

// buildSSDPResponse builds the unicast reply to an M-SEARCH for the device
// type, pointing at this device's /device.xml under locationBase (AC1).
func buildSSDPResponse(locationBase, deviceUUID string) string {
	return buildSSDPResponseST(locationBase, deviceUUID, ssdpST)
}

// buildSSDPResponseST is the M-SEARCH reply for one search target.
// locationBase must be absolute (http://host:port): clients fetch LOCATION
// as is, and a bare "/device.xml" is unusable.
func buildSSDPResponseST(locationBase, deviceUUID, st string) string {
	return strings.Join([]string{
		"HTTP/1.1 200 OK",
		"CACHE-CONTROL: max-age=1800",
		"EXT:",
		"LOCATION: " + strings.TrimRight(locationBase, "/") + "/device.xml",
		"SERVER: " + ssdpServer,
		"ST: " + st,
		"USN: " + ssdpUSN(deviceUUID, st),
		"", "",
	}, "\r\n")
}

// buildSSDPNotify is a multicast NOTIFY for one target: ssdp:alive with the
// LOCATION, or ssdp:byebye.
func buildSSDPNotify(locationBase, deviceUUID, nt string, alive bool) string {
	lines := []string{"NOTIFY * HTTP/1.1", "HOST: " + ssdpAddr}
	if alive {
		lines = append(lines,
			"CACHE-CONTROL: max-age=1800",
			"LOCATION: "+strings.TrimRight(locationBase, "/")+"/device.xml",
			"SERVER: "+ssdpServer,
			"NTS: ssdp:alive")
	} else {
		lines = append(lines, "NTS: ssdp:byebye")
	}
	lines = append(lines, "NT: "+nt, "USN: "+ssdpUSN(deviceUUID, nt), "", "")
	return strings.Join(lines, "\r\n")
}

// notifyTargets are the NTs a root device announces.
func notifyTargets(deviceUUID string) []string {
	return []string{ssdpRootDevice, "uuid:" + deviceUUID, ssdpST}
}

// LocalIface is one LAN interface address announcements go out on.
type LocalIface struct {
	Ifi net.Interface
	IP  net.IP
}

// lanIfaces lists the up, multicast-capable, non-loopback IPv4 addresses
// (link-local 169.254/16 skipped).
func lanIfaces() []LocalIface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []LocalIface
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLinkLocalUnicast() || n.IP.IsLoopback() {
				continue
			}
			out = append(out, LocalIface{Ifi: ifi, IP: n.IP.To4()})
		}
	}
	return out
}

// ListenSSDP opens the production SSDP socket: udp/1900 joined to the group.
func ListenSSDP() (net.PacketConn, error) {
	addr, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, err
	}
	return net.ListenMulticastUDP("udp4", nil, addr)
}

// Responder listens for SSDP M-SEARCH datagrams and replies with our
// device location; it also multicasts ssdp:alive on start and every
// NotifyEvery, and ssdp:byebye when it stops. It is started only when the
// feature is enabled (AC9); when off it is never constructed and the LAN sees
// nothing.
type Responder struct {
	// Conn is the socket to serve; nil opens ListenSSDP (all interfaces).
	// Tests pass a loopback socket.
	Conn net.PacketConn
	// DeviceUUID is the persisted UPnP UDN.
	DeviceUUID string
	// Advertiser picks the absolute LOCATION base per client / interface.
	Advertiser Advertiser
	// NotifyEvery is the alive interval (0: DefaultNotifyEvery; <0: never
	// announce).
	NotifyEvery time.Duration
	// Ifaces lists the interfaces to announce on (nil: lanIfaces).
	Ifaces func() []LocalIface
	// Multicast sends one NOTIFY out of an interface (nil: through Conn's
	// multicast interface option, when Conn is a UDP socket).
	Multicast func(on LocalIface, msg []byte) error
}

// Run blocks until ctx is cancelled, answering each matching M-SEARCH
// unicast. Errors binding the socket are returned (the caller logs and
// retries — SSDP is best-effort over Docker bridges, see deploy notes).
func (s *Responder) Run(ctx context.Context) error {
	conn := s.Conn
	if conn == nil {
		var err error
		if conn, err = ListenSSDP(); err != nil {
			return err
		}
	}
	defer conn.Close()
	ifaces := s.Ifaces
	if ifaces == nil {
		ifaces = lanIfaces
	}
	send := s.Multicast
	if uc, ok := conn.(*net.UDPConn); ok && send == nil {
		send = multicastSender(uc, ifaces())
	}

	announce := func(alive bool) {
		if send == nil {
			return
		}
		for _, li := range ifaces() {
			base := s.Advertiser.BaseForLocal(li.IP)
			for _, nt := range notifyTargets(s.DeviceUUID) {
				_ = send(li, []byte(buildSSDPNotify(base, s.DeviceUUID, nt, alive)))
			}
		}
	}

	// The announcer owns shutdown: byebye goes out before the socket closes.
	ctx, cancel := context.WithCancel(ctx)
	notifyDone := make(chan struct{})
	go func() {
		defer close(notifyDone)
		every := s.NotifyEvery
		if every == 0 {
			every = DefaultNotifyEvery
		}
		if every > 0 {
			announce(true)
			t := time.NewTicker(every)
			defer t.Stop()
		loop:
			for {
				select {
				case <-ctx.Done():
					break loop
				case <-t.C:
					announce(true)
				}
			}
			announce(false)
		} else {
			<-ctx.Done()
		}
		_ = conn.Close()
	}()
	defer func() { cancel(); <-notifyDone }()

	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			select { // a transient read error: retry without spinning
			case <-ctx.Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		targets := msearchTargets(string(buf[:n]), s.DeviceUUID)
		if len(targets) == 0 {
			continue
		}
		base := s.Advertiser.BaseFor(remoteIP(src))
		for _, st := range targets {
			_, _ = conn.WriteTo([]byte(buildSSDPResponseST(base, s.DeviceUUID, st)), src)
		}
	}
}

// multicastSender joins the SSDP group on every LAN interface (not just the
// default route's) and returns a sender that multicasts out of a chosen one.
func multicastSender(uc *net.UDPConn, lans []LocalIface) func(LocalIface, []byte) error {
	pc := ipv4.NewPacketConn(uc)
	group := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250)}
	for _, li := range lans {
		ifi := li.Ifi
		_ = pc.JoinGroup(&ifi, group) // already joined on the default one
	}
	_ = pc.SetMulticastTTL(2) // UPnP Device Architecture 1.1: TTL 2
	dst := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	var mu sync.Mutex
	return func(on LocalIface, msg []byte) error {
		mu.Lock()
		defer mu.Unlock()
		ifi := on.Ifi
		if err := pc.SetMulticastInterface(&ifi); err != nil {
			return err
		}
		_, err := pc.WriteTo(msg, nil, dst)
		return err
	}
}
