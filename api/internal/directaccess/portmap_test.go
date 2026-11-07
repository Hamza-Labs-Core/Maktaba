package directaccess

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePMP is a router answering NAT-PMP and PCP on one UDP port, as RFC 6886 and 6887 say.
type fakePMP struct {
	conn     net.PacketConn
	external netip.Addr
	mu       sync.Mutex
	mapped   map[int]int // internal → external
	removed  []int
	pcp      bool // answer PCP only (NAT-PMP gets result 1, unsupported version)
}

func newFakePMP(t *testing.T, external string, pcp bool) *fakePMP {
	t.Helper()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePMP{conn: c, external: netip.MustParseAddr(external), mapped: map[int]int{}, pcp: pcp}
	go f.serve()
	t.Cleanup(func() { _ = c.Close() })
	return f
}

func (f *fakePMP) addr() netip.AddrPort {
	return netip.MustParseAddrPort(f.conn.LocalAddr().String())
}

func (f *fakePMP) serve() {
	buf := make([]byte, 1100)
	for {
		n, from, err := f.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		req := append([]byte(nil), buf[:n]...)
		var resp []byte
		switch {
		case req[0] == 0 && f.pcp:
			resp = []byte{0, 128 + req[1], 0, 1} // unsupported version
		case req[0] == 0 && req[1] == 0:
			resp = make([]byte, 12)
			resp[1] = 128
			ip := f.external.As4()
			copy(resp[8:12], ip[:])
		case req[0] == 0 && req[1] == 2:
			internal := int(binary.BigEndian.Uint16(req[4:6]))
			ext := int(binary.BigEndian.Uint16(req[6:8]))
			life := binary.BigEndian.Uint32(req[8:12])
			f.mu.Lock()
			if life == 0 {
				f.removed = append(f.removed, internal)
				delete(f.mapped, internal)
			} else {
				if ext == 0 {
					ext = 40000
				}
				f.mapped[internal] = ext
			}
			f.mu.Unlock()
			resp = make([]byte, 16)
			resp[1] = 130
			binary.BigEndian.PutUint16(resp[8:10], uint16(internal))
			binary.BigEndian.PutUint16(resp[10:12], uint16(ext))
			binary.BigEndian.PutUint32(resp[12:16], life)
		case req[0] == 2 && req[1] == 1 && n >= 60:
			internal := int(binary.BigEndian.Uint16(req[40:42]))
			ext := int(binary.BigEndian.Uint16(req[42:44]))
			life := binary.BigEndian.Uint32(req[4:8])
			f.mu.Lock()
			if life == 0 {
				f.removed = append(f.removed, internal)
				delete(f.mapped, internal)
			} else {
				f.mapped[internal] = ext
			}
			f.mu.Unlock()
			resp = make([]byte, 60)
			resp[0], resp[1] = 2, 0x81
			binary.BigEndian.PutUint32(resp[4:8], life)
			copy(resp[24:44], req[24:44]) // nonce, protocol, internal port
			binary.BigEndian.PutUint16(resp[42:44], uint16(ext))
			ip := netip.AddrFrom4(f.external.As4()).As16()
			copy(resp[44:60], ip[:])
		default:
			continue
		}
		_, _ = f.conn.WriteTo(resp, from)
	}
}

func (f *fakePMP) state() (map[int]int, []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[int]int{}
	for k, v := range f.mapped {
		m[k] = v
	}
	return m, append([]int(nil), f.removed...)
}

// T04: NAT-PMP maps, reports the external address, and removes the mapping.
func TestNATPMP(t *testing.T) {
	gw := newFakePMP(t, "81.2.3.4", false)
	n := &NATPMP{Gateway: gw.addr()}
	ctx := context.Background()
	m, err := n.Map(ctx, 8444, 8444, Lease)
	if err != nil || m.External != 8444 || m.ExternalIP != netip.MustParseAddr("81.2.3.4") || m.Lifetime != Lease {
		t.Fatalf("map: %+v %v", m, err)
	}
	if err := n.Unmap(ctx, m); err != nil {
		t.Fatal(err)
	}
	if mapped, removed := gw.state(); len(mapped) != 0 || len(removed) != 1 {
		t.Fatalf("after unmap: %v %v", mapped, removed)
	}
}

// T04: PCP's MAP, for a router that speaks PCP and not NAT-PMP.
func TestPCP(t *testing.T) {
	gw := newFakePMP(t, "81.2.3.4", true)
	ctx := context.Background()
	if _, err := (&NATPMP{Gateway: gw.addr(), Tries: 1}).Map(ctx, 8444, 8444, Lease); err == nil {
		t.Fatal("NAT-PMP worked on a PCP-only router")
	}
	p := &PCP{Gateway: gw.addr()}
	m, err := p.Map(ctx, 8444, 31000, Lease)
	if err != nil || m.External != 31000 || m.ExternalIP != netip.MustParseAddr("81.2.3.4") {
		t.Fatalf("map: %+v %v", m, err)
	}
	if err := p.Unmap(ctx, m); err != nil {
		t.Fatal(err)
	}
	if mapped, _ := gw.state(); len(mapped) != 0 {
		t.Fatalf("still mapped: %v", mapped)
	}
}

// fakeIGD is a UPnP Internet Gateway Device: an SSDP responder and the HTTP description and
// control URLs.
type fakeIGD struct {
	ssdp     net.PacketConn
	http     *httptest.Server
	external string
	mu       sync.Mutex
	calls    []string // action ext-port internal-client
}

func newFakeIGD(t *testing.T, external string) *fakeIGD {
	t.Helper()
	f := &fakeIGD{external: external}
	f.http = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.http.Close)
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ssdp = c
	t.Cleanup(func() { _ = c.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			if strings.HasPrefix(string(buf[:n]), "M-SEARCH") && strings.Contains(string(buf[:n]), "InternetGatewayDevice:1") {
				msg := "HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\nLOCATION: " +
					f.http.URL + "/desc.xml\r\nUSN: uuid:fake\r\n\r\n"
				_, _ = c.WriteTo([]byte(msg), from)
			}
		}
	}()
	return f
}

func (f *fakeIGD) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/desc.xml":
		fmt.Fprint(w, `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><device>
<deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType><deviceList><device>
<deviceType>urn:schemas-upnp-org:device:WANDevice:1</deviceType><deviceList><device>
<deviceType>urn:schemas-upnp-org:device:WANConnectionDevice:1</deviceType><serviceList><service>
<serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType><controlURL>/ctl/IPConn</controlURL>
</service></serviceList></device></deviceList></device></deviceList></device></root>`)
	case "/ctl/IPConn":
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		action := strings.TrimSuffix(strings.SplitN(r.Header.Get("SOAPAction"), "#", 2)[1], `"`)
		field := func(name string) string {
			i := strings.Index(body, "<"+name+">")
			j := strings.Index(body, "</"+name+">")
			if i < 0 || j < 0 {
				return ""
			}
			return body[i+len(name)+2 : j]
		}
		f.mu.Lock()
		f.calls = append(f.calls, action+" "+field("NewExternalPort")+" "+field("NewInternalClient")+" "+field("NewLeaseDuration"))
		f.mu.Unlock()
		if action == "GetExternalIPAddress" {
			fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"><NewExternalIPAddress>%s</NewExternalIPAddress></u:GetExternalIPAddressResponse></s:Body></s:Envelope>`, f.external)
			return
		}
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/></s:Body></s:Envelope>`, action)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeIGD) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// T04: UPnP-IGD found by SSDP, the WAN service found in the description, the mapping added for
// this machine's address with a one-hour lease, and deleted.
func TestUPnP(t *testing.T) {
	igd := newFakeIGD(t, "81.2.3.4")
	u := &UPnP{SSDP: igd.ssdp.LocalAddr().String(), Wait: 2 * time.Second}
	ctx := context.Background()
	m, err := u.Map(ctx, 8444, 8444, Lease)
	if err != nil || m.External != 8444 || m.ExternalIP != netip.MustParseAddr("81.2.3.4") {
		t.Fatalf("map: %+v %v", m, err)
	}
	if err := u.Unmap(ctx, m); err != nil {
		t.Fatal(err)
	}
	calls := igd.Calls()
	if len(calls) != 3 || calls[0] != "AddPortMapping 8444 127.0.0.1 3600" || !strings.HasPrefix(calls[1], "GetExternalIPAddress") ||
		calls[2] != "DeletePortMapping 8444  " {
		t.Fatalf("calls %q", calls)
	}
}

// MapFirst takes the first mapper that works, in order.
func TestMapFirstGoesInOrder(t *testing.T) {
	gw := newFakePMP(t, "81.2.3.4", false)
	nothing := &UPnP{SSDP: "127.0.0.1:9", Wait: 200 * time.Millisecond}
	m, by, err := MapFirst(context.Background(), []Mapper{nothing, &NATPMP{Gateway: gw.addr()}}, 8444, 8444)
	if err != nil || by.Name() != "natpmp" || m.External != 8444 {
		t.Fatalf("%+v %v %v", m, by, err)
	}
	if _, _, err := MapFirst(context.Background(), []Mapper{nothing}, 8444, 8444); err == nil {
		t.Fatal("no router, no error")
	}
}

func TestShared(t *testing.T) {
	for ip, want := range map[string]bool{"100.64.1.2": true, "192.168.1.1": true, "10.0.0.1": true, "81.2.3.4": false, "2001:db8::1": false} {
		if Shared(netip.MustParseAddr(ip)) != want {
			t.Errorf("%s: want %v", ip, want)
		}
	}
}
