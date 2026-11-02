//go:build linux

package discovery

// The announcement for real, on a Linux whose systemd-resolved answers mDNS: CI's
// mdns-linux job (_unit.yml) turns resolved's mDNS on, stops Avahi, and runs this as root
// (RegisterService), as a user a polkit rule lets register (RegisterService too), and as a
// user it does not ("<host>-maktaba" through zeroconf). An mDNS client of its own, on its
// own socket, asks the network what a phone would: the PTR of _maktaba._tcp, the
// instance's SRV and TXT, the target's address; and resolvectl asks resolved the same.
// MAKTABA_TEST_LIVE_MDNS names the path expected ("resolved" or "own-host"); without it
// the test does not run, so a developer's machine never announces by accident.

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const liveService = "_maktaba._tcp"

func TestAnnounceLive(t *testing.T) {
	mode := os.Getenv("MAKTABA_TEST_LIVE_MDNS")
	if mode == "" {
		t.Skip("MAKTABA_TEST_LIVE_MDNS is not set: the mdns-linux CI job runs this")
	}
	r := systemResponder()
	t.Logf("euid %d, responder %+v, resolved %+v", os.Geteuid(), r, probeResolved())
	h, _ := os.Hostname()
	host := strings.ToLower(bareHost(h))
	switch mode {
	case "resolved":
		if r.kind != viaResolved {
			t.Fatalf("responder %+v, want systemd-resolved", r)
		}
		if !strings.EqualFold(r.host, host) {
			t.Fatalf("resolved answers for %q, the machine is %q", r.host, host)
		}
	case "own-host":
		if r.kind != viaZeroconfOwnHost {
			t.Fatalf("responder %+v, want zeroconf under a host of its own", r)
		}
		host += ownHostSuffix
	default:
		t.Fatalf("MAKTABA_TEST_LIVE_MDNS=%q: want resolved or own-host", mode)
	}
	if got := strings.ToLower(announcedHost(r)); got != host {
		t.Fatalf("announced host %q, want %q", got, host)
	}

	ifi := liveInterface(t)
	c := newLiveClient(t, ifi)
	run := strconv.Itoa(rand.IntN(1e6))
	for _, name := range []string{
		"Maktaba CI " + run,
		"Studio 1.2 at 100% " + run, // a dot, and a % (resolved's specifier)
		`Back\slash "home" ` + run,  // a backslash, a quote
		"مكتبة البيت " + run,        // Arabic
	} {
		t.Run(name, func(t *testing.T) { liveAnnounce(t, c, ifi, name, host) })
	}
}

// liveAnnounce announces name, finds it as a phone would, changes its TXT, withdraws it.
func liveAnnounce(t *testing.T, c *liveClient, ifi *net.Interface, name, host string) {
	port := 20000 + rand.IntN(20000)
	txt := []string{"id=0a1b2c3d", "role=home", "version=1.0.0-ci", "path=/m"}
	a, err := register(name, liveService, "local.", port, txt)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	withdrawn := false
	defer func() {
		if !withdrawn {
			a.Shutdown()
		}
	}()

	instance := zeroconfInstance(name) + "." + liveService + ".local."
	target := host + ".local."

	c.await(t, "the PTR names it", liveService+".local.", dns.TypePTR, func(rrs []dns.RR) bool {
		return slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			p, ok := rr.(*dns.PTR)
			return ok && sameName(p.Ptr, instance)
		})
	})
	c.await(t, fmt.Sprintf("its SRV is %s:%d", target, port), instance, dns.TypeSRV, func(rrs []dns.RR) bool {
		return slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			s, ok := rr.(*dns.SRV)
			return ok && sameName(s.Hdr.Name, instance) && int(s.Port) == port && sameName(s.Target, target)
		})
	})
	c.await(t, fmt.Sprintf("its TXT is %q", txt), instance, dns.TypeTXT, hasTXT(instance, txt))
	addrs := liveAddrs(t, ifi)
	c.await(t, fmt.Sprintf("%s is one of %v", target, addrs), target, dns.TypeA, func(rrs []dns.RR) bool {
		return slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			x, ok := rr.(*dns.A)
			return ok && sameName(x.Hdr.Name, target) && slices.Contains(addrs, x.A.String())
		})
	})
	resolvectlService(t, name, target, port, txt)

	// a new TXT replaces the old one
	txt2 := []string{"id=0a1b2c3d", "role=home", "version=1.0.1-ci", "path=/m"}
	a.SetText(txt2)
	c.await(t, fmt.Sprintf("its TXT is now %q", txt2), instance, dns.TypeTXT, func(rrs []dns.RR) bool {
		return hasTXT(instance, txt2)(rrs) && !hasTXT(instance, txt)(rrs)
	})

	a.Shutdown()
	withdrawn = true
	c.await(t, "withdrawn: no PTR names it", liveService+".local.", dns.TypePTR, func(rrs []dns.RR) bool {
		return !slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			p, ok := rr.(*dns.PTR)
			return ok && sameName(p.Ptr, instance) && p.Hdr.Ttl > 0
		})
	})
	c.await(t, "withdrawn: no SRV for it", instance, dns.TypeSRV, func(rrs []dns.RR) bool {
		return !slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			s, ok := rr.(*dns.SRV)
			return ok && sameName(s.Hdr.Name, instance) && s.Hdr.Ttl > 0
		})
	})
}

func hasTXT(instance string, want []string) func([]dns.RR) bool {
	sorted := slices.Sorted(slices.Values(want))
	return func(rrs []dns.RR) bool {
		return slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			x, ok := rr.(*dns.TXT)
			return ok && sameName(x.Hdr.Name, instance) && x.Hdr.Ttl > 0 &&
				slices.Equal(slices.Sorted(slices.Values(x.Txt)), sorted)
		})
	}
}

// sameName compares two names label by label, as mDNS does (ASCII case aside), whatever
// escapes their presentation uses.
func sameName(a, b string) bool {
	wa, wb := make([]byte, 256), make([]byte, 256)
	na, errA := dns.PackDomainName(dns.Fqdn(a), wa, 0, nil, false)
	nb, errB := dns.PackDomainName(dns.Fqdn(b), wb, 0, nil, false)
	return errA == nil && errB == nil && bytes.Equal(asciiLower(wa[:na]), asciiLower(wb[:nb]))
}

func asciiLower(b []byte) []byte {
	out := bytes.Clone(b)
	for i, c := range out {
		if 'A' <= c && c <= 'Z' {
			out[i] = c + 'a' - 'A'
		}
	}
	return out
}

// liveInterface is the interface the test asks on: MAKTABA_TEST_MDNS_IFACE, else the first
// up, multicast, non-loopback one with an IPv4.
func liveInterface(t *testing.T) *net.Interface {
	t.Helper()
	if name := os.Getenv("MAKTABA_TEST_MDNS_IFACE"); name != "" {
		ifi, err := net.InterfaceByName(name)
		if err != nil {
			t.Fatalf("interface %s: %v", name, err)
		}
		return ifi
	}
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		ifi := &ifaces[i]
		if ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagMulticast != 0 && ifi.Flags&net.FlagLoopback == 0 && len(liveAddrs(t, ifi)) > 0 {
			return ifi
		}
	}
	t.Fatal("no multicast interface with an IPv4")
	return nil
}

func liveAddrs(t *testing.T, ifi *net.Interface) []string {
	t.Helper()
	addrs, err := ifi.Addrs()
	if err != nil {
		t.Fatalf("%s: %v", ifi.Name, err)
	}
	var out []string
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// liveClient asks over multicast DNS from a socket of its own: bound to 5353 in the group,
// as a phone's responder-less client does, so answers sent to the group reach it, with the
// loop on so a responder on this machine hears it.
type liveClient struct {
	conn *net.UDPConn
	ifi  *net.Interface
}

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

func newLiveClient(t *testing.T, ifi *net.Interface) *liveClient {
	t.Helper()
	conn, err := net.ListenMulticastUDP("udp4", ifi, mdnsGroup)
	if err != nil {
		t.Fatalf("listen on the mDNS group on %s: %v", ifi.Name, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		// ListenMulticastUDP turns the loop off: a responder on this machine would never
		// hear the question
		serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
	}); err != nil || serr != nil {
		t.Fatalf("multicast loop: %v %v", err, serr)
	}
	return &liveClient{conn: conn, ifi: ifi}
}

// ask sends one question and gathers every record answered within a second.
func (c *liveClient) ask(t *testing.T, name string, qtype uint16) []dns.RR {
	t.Helper()
	m := new(dns.Msg)
	m.Id = 0
	m.RecursionDesired = false
	m.Question = []dns.Question{{Name: dns.Fqdn(name), Qtype: qtype, Qclass: dns.ClassINET}}
	b, err := m.Pack()
	if err != nil {
		t.Fatalf("pack %s: %v", name, err)
	}
	if _, err := c.conn.WriteToUDP(b, mdnsGroup); err != nil {
		t.Fatalf("send: %v", err)
	}
	var out []dns.RR
	buf := make([]byte, 9000)
	end := time.Now().Add(time.Second)
	for {
		_ = c.conn.SetReadDeadline(end)
		n, _, err := c.conn.ReadFromUDP(buf)
		if err != nil {
			return out
		}
		var r dns.Msg
		if r.Unpack(buf[:n]) != nil || !r.Response {
			continue
		}
		out = append(append(out, r.Answer...), r.Extra...)
	}
}

// await asks until want holds of the answers, for up to 15 seconds.
func (c *liveClient) await(t *testing.T, what, name string, qtype uint16, want func([]dns.RR) bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rrs := c.ask(t, name, qtype)
		if want(rrs) {
			t.Logf("%s: yes", what)
			return
		}
		if time.Now().After(deadline) {
			var got []string
			for _, rr := range rrs {
				got = append(got, rr.String())
			}
			t.Fatalf("%s: no; asked %s %s on %s, heard:\n%s", what, dns.TypeToString[qtype], name, c.ifi.Name, strings.Join(got, "\n"))
		}
	}
}

// resolvectlService asks resolved for the service (`resolvectl service`), a second client:
// resolved answers its own registrations, and asks the network for zeroconf's.
func resolvectlService(t *testing.T, name, target string, port int, txt []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "resolvectl", "service", name, liveService, "local").CombinedOutput()
	t.Logf("resolvectl service %q %s local:\n%s", name, liveService, out)
	if err != nil {
		t.Fatalf("resolvectl service: %v", err)
	}
	s := strings.ToLower(string(out))
	if want := strings.ToLower(strings.TrimSuffix(target, ".")) + ":" + strconv.Itoa(port); !strings.Contains(s, want) {
		t.Errorf("resolvectl does not say %s", want)
	}
	for _, x := range txt {
		if !strings.Contains(string(out), x) {
			t.Errorf("resolvectl does not say TXT %s", x)
		}
	}
}
