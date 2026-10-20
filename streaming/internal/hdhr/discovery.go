package hdhr

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"
)

// DiscoverResponder answers HDHomeRun discover requests (UDP 65001, see
// hdhrpkt.go) the way a real tuner does: a unicast DISCOVER_RPY to the asker
// with the tuner type, DeviceID, TunerCount, BaseURL and LineupURL. It runs
// next to the SSDP responder under the same controller (SSDPController), so
// it only listens while the tuner is enabled on the host that owns it.
type DiscoverResponder struct {
	// Conn is the socket to serve; nil listens on udp4 :65001 (all
	// interfaces: broadcast discovery needs it). Tests pass a loopback socket.
	Conn       net.PacketConn
	Device     Device
	DeviceAuth string
	Advertiser Advertiser
}

// ListenDiscover opens the production discover socket.
func ListenDiscover() (net.PacketConn, error) {
	return net.ListenPacket("udp4", ":"+strconv.Itoa(HDHRDiscoverPort))
}

// Run serves until ctx ends; the socket is closed on return.
func (d *DiscoverResponder) Run(ctx context.Context) error {
	id, err := ParseDeviceID(d.Device.DeviceID)
	if err != nil {
		return err
	}
	conn := d.Conn
	if conn == nil {
		if conn, err = ListenDiscover(); err != nil {
			return err
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()

	buf := make([]byte, 1500)
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			// a Windows ICMP "port unreachable" from an earlier reply
			// surfaces as a read error on UDP; keep serving, without spinning
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		base := d.Advertiser.BaseFor(remoteIP(src))
		reply := handleDiscoverPacket(buf[:n], hdhrDiscoverInfo{
			DeviceID:   id,
			TunerCount: d.Device.TunerCount,
			BaseURL:    base,
			LineupURL:  base + "/lineup.json",
			DeviceAuth: d.DeviceAuth,
		})
		if reply != nil {
			_, _ = conn.WriteTo(reply, src)
		}
	}
}
