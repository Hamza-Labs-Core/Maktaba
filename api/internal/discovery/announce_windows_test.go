//go:build windows

package discovery

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// Windows 10+ (the CI runner among them) has the registration API, so a home there
// announces through the system's responder.
func TestWindowsHasDNSAPI(t *testing.T) {
	if !dnsapiAvailable() {
		t.Fatal("dnsapi.dll has no DnsServiceRegister")
	}
	if got := systemResponder(); got.kind != viaDNSAPI {
		t.Fatalf("responder = %+v, want dnsapi", got)
	}
}

// The real dnsapi.dll takes a registration and answers it; withdrawing it is answered
// too, and lets go of it.
func TestWindowsDNSAPIRegistersAndWithdraws(t *testing.T) {
	svc := winService{
		instance: "Maktaba test " + time.Now().Format("150405.000000") + "._maktaba-test._tcp.local",
		host:     announcedHost(systemResponder()) + ".local",
		port:     18088,
		keys:     []string{"role", "web"},
		values:   []string{"home", "8088"},
	}
	svc.ip4, svc.ip6 = firstIPs(interfaceIPs())
	answered := make(chan error, 1)
	reg, err := windowsDNSAPI{}.register(svc, func(err error) { answered <- err })
	if err != nil {
		t.Fatalf("DnsServiceRegister: %v", err)
	}
	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("registration refused: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no answer to the registration")
	}
	r := reg.(*winReg)
	reg.deregister()
	select {
	case <-r.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the withdrawal was not answered")
	}
}

// A home name with a "." (and a "\", and Arabic) goes on the wire as one label: the
// escaped name DnsServiceConstructInstance gets is the one Windows answers with. Asked as
// a legacy unicast query (from a port other than 5353), the responder answers this
// process directly.
func TestWindowsDNSAPIKeepsADottedNameOneLabel(t *testing.T) {
	const service = "_maktaba-dot._tcp"
	name := "Maktaba v1.2 a\\b مكتبة " + time.Now().Format("150405.000000")
	a, err := startDNSAPI(windowsDNSAPI{}, name, service, "local.", announcedHost(systemResponder())+".local", 18087,
		interfaceIPs(), []string{"role=home"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	q := new(dns.Msg)
	q.SetQuestion(service+".local.", dns.TypePTR)
	q.Id = 0x4d4b
	wire, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	want := zeroconfInstance(name) + "." + service + ".local." // the label as the DNS library writes it
	var seen []string
	buf := make([]byte, 9000)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := conn.WriteToUDP(wire, group); err != nil {
			t.Fatalf("query: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			m := new(dns.Msg)
			if m.Unpack(buf[:n]) != nil {
				continue
			}
			for _, rr := range append(m.Answer, m.Extra...) {
				ptr, ok := rr.(*dns.PTR)
				if !ok || !strings.EqualFold(ptr.Hdr.Name, service+".local.") {
					continue
				}
				if ptr.Ptr == want {
					return
				}
				seen = append(seen, ptr.Ptr)
			}
		}
	}
	t.Fatalf("no answer naming %q; answers: %q", want, seen)
}

// Through the announcement: a new TXT and Shutdown on the real API, withdrawn when done.
func TestWindowsAnnouncementChangesAndShutsDown(t *testing.T) {
	a, err := register("Maktaba test "+time.Now().Format("150405.000000"), "_maktaba-test._tcp", "local.", 18089, []string{"web=8088"})
	if err != nil {
		t.Fatal(err)
	}
	d, ok := a.(*systemDNSAPI)
	if !ok {
		t.Fatalf("announcement is %T, want the system's", a)
	}
	a.SetText([]string{"web=8090"})
	a.Shutdown()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reg != nil || !d.stopped {
		t.Fatal("still registered after Shutdown")
	}
}
