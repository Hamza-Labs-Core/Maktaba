//go:build linux

package mdns

// The announcement for real, on a Linux whose systemd-resolved answers mDNS: CI's
// mdns-linux job (_unit.yml, tools/ci/mdns-linux.sh) turns resolved's mDNS on, masks
// Avahi, and runs this as root (RegisterService), as a user a polkit rule lets register
// (RegisterService too), and as a user it does not ("<host>-maktaba" through zeroconf).
// An mDNS client asks what a phone would (the PTR of _maktaba._tcp, the instance's SRV and
// TXT, the target's address) from what resolved takes for another machine: a network
// namespace at the other end of a veth (MAKTABA_TEST_MDNS_PEER_NETNS, its link
// MAKTABA_TEST_MDNS_PEER_IFACE; this end is MAKTABA_TEST_MDNS_IFACE), since resolved
// ignores every mDNS packet from an address of its own machine (resolved-mdns.c,
// on_mdns_packet), questions and answers alike. resolvectl asks resolved as well, where
// resolved announces. MAKTABA_TEST_LIVE_MDNS names the path expected ("resolved" or
// "own-host"); without it the test does not run, so a developer's machine never announces
// by accident.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
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
	c := newPeerClient(t)
	run := strconv.Itoa(rand.IntN(1e6))
	for _, name := range []string{
		"Maktaba CI " + run,
		"Studio 1.2 at 100% " + run, // a dot, and a % (resolved's specifier)
		`Back\slash "home" ` + run,  // a backslash, a quote
		"مكتبة البيت " + run,        // Arabic
	} {
		t.Run(name, func(t *testing.T) { liveAnnounce(t, c, ifi, name, host, mode == "resolved") })
	}
}

// liveAnnounce announces name, finds it as a phone would, changes its TXT, withdraws it.
// askResolved: resolvectl asks resolved too, which answers what was registered with it;
// what zeroconf answers on this machine resolved never hears (it ignores its own
// addresses), so there only the peer asks.
func liveAnnounce(t *testing.T, c asker, ifi *net.Interface, name, host string, askResolved bool) {
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

	await(t, c, "the PTR names it", liveService+".local.", dns.TypePTR, func(rrs []dns.RR) bool {
		return slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			p, ok := rr.(*dns.PTR)
			return ok && sameName(p.Ptr, instance)
		})
	})
	await(t, c, fmt.Sprintf("its SRV is %s:%d", target, port), instance, dns.TypeSRV, func(rrs []dns.RR) bool {
		return slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			s, ok := rr.(*dns.SRV)
			return ok && sameName(s.Hdr.Name, instance) && int(s.Port) == port && sameName(s.Target, target)
		})
	})
	await(t, c, fmt.Sprintf("its TXT is %q", txt), instance, dns.TypeTXT, hasTXT(instance, txt))
	addrs := liveAddrs(t, ifi)
	await(t, c, fmt.Sprintf("%s is one of %v", target, addrs), target, dns.TypeA, func(rrs []dns.RR) bool {
		return slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			x, ok := rr.(*dns.A)
			return ok && sameName(x.Hdr.Name, target) && slices.Contains(addrs, x.A.String())
		})
	})
	if askResolved {
		resolvectlService(t, name, target, port, txt)
	}

	// a new TXT replaces the old one
	txt2 := []string{"id=0a1b2c3d", "role=home", "version=1.0.1-ci", "path=/m"}
	a.SetText(txt2)
	await(t, c, fmt.Sprintf("its TXT is now %q", txt2), instance, dns.TypeTXT, func(rrs []dns.RR) bool {
		return hasTXT(instance, txt2)(rrs) && !hasTXT(instance, txt)(rrs)
	})

	a.Shutdown()
	withdrawn = true
	await(t, c, "withdrawn: no PTR names it", liveService+".local.", dns.TypePTR, func(rrs []dns.RR) bool {
		return !slices.ContainsFunc(rrs, func(rr dns.RR) bool {
			p, ok := rr.(*dns.PTR)
			return ok && sameName(p.Ptr, instance) && p.Hdr.Ttl > 0
		})
	})
	await(t, c, "withdrawn: no SRV for it", instance, dns.TypeSRV, func(rrs []dns.RR) bool {
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

// liveInterface is this end of the link the peer asks on (MAKTABA_TEST_MDNS_IFACE).
func liveInterface(t *testing.T) *net.Interface {
	t.Helper()
	name := os.Getenv("MAKTABA_TEST_MDNS_IFACE")
	if name == "" {
		t.Fatal("MAKTABA_TEST_MDNS_IFACE names the link the peer asks on (tools/ci/mdns-linux.sh)")
	}
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatalf("interface %s: %v", name, err)
	}
	return ifi
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

// asker asks one question over multicast DNS and gives back every record heard within a
// second.
type asker interface {
	ask(t *testing.T, name string, qtype uint16) []dns.RR
}

// liveClient asks from a socket of its own: bound to 5353 in the group, as a phone's
// responder-less client does, so answers sent to the group reach it.
type liveClient struct {
	conn *net.UDPConn
}

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

func newLiveClient(ifi *net.Interface) (*liveClient, error) {
	conn, err := net.ListenMulticastUDP("udp4", ifi, mdnsGroup)
	if err != nil {
		return nil, fmt.Errorf("listen on the mDNS group on %s: %w", ifi.Name, err)
	}
	return &liveClient{conn: conn}, nil
}

func (c *liveClient) query(name string, qtype uint16) ([]dns.RR, error) {
	m := new(dns.Msg)
	m.Question = []dns.Question{{Name: dns.Fqdn(name), Qtype: qtype, Qclass: dns.ClassINET}}
	b, err := m.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", name, err)
	}
	if _, err := c.conn.WriteToUDP(b, mdnsGroup); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	var out []dns.RR
	buf := make([]byte, 9000)
	end := time.Now().Add(time.Second)
	for {
		_ = c.conn.SetReadDeadline(end)
		n, _, err := c.conn.ReadFromUDP(buf)
		if err != nil {
			return out, nil
		}
		var r dns.Msg
		if r.Unpack(buf[:n]) != nil || !r.Response {
			continue
		}
		out = append(append(out, r.Answer...), r.Extra...)
	}
}

// peerClient is a liveClient in the peer's namespace: this test binary again
// (TestMDNSPeerHelper), through sudo and ip netns exec; a question a line on its stdin
// ("<type> <name>"), the records heard a line each on its stdout ("rr <record>", then
// "end").
type peerClient struct {
	in  io.WriteCloser
	out *bufio.Scanner
}

func newPeerClient(t *testing.T) *peerClient {
	t.Helper()
	ns, ifname := os.Getenv("MAKTABA_TEST_MDNS_PEER_NETNS"), os.Getenv("MAKTABA_TEST_MDNS_PEER_IFACE")
	if ns == "" || ifname == "" {
		t.Fatal("MAKTABA_TEST_MDNS_PEER_NETNS and MAKTABA_TEST_MDNS_PEER_IFACE name the peer (tools/ci/mdns-linux.sh)")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sudo", "-n", "ip", "netns", "exec", ns, "env", "MAKTABA_TEST_MDNS_PEER="+ifname,
		self, "-test.run", "^TestMDNSPeerHelper$", "-test.count=1")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the peer: %v", err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Wait()
	})
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	return &peerClient{in: in, out: sc}
}

func (p *peerClient) ask(t *testing.T, name string, qtype uint16) []dns.RR {
	t.Helper()
	if _, err := fmt.Fprintf(p.in, "%d %s\n", qtype, name); err != nil {
		t.Fatalf("ask the peer: %v", err)
	}
	var out []dns.RR
	for p.out.Scan() {
		line := p.out.Text()
		switch {
		case line == "end":
			return out
		case strings.HasPrefix(line, "rr "):
			rr, err := dns.NewRR(strings.TrimPrefix(line, "rr "))
			if err != nil {
				t.Fatalf("the peer heard %q: %v", line, err)
			}
			out = append(out, rr)
		case strings.HasPrefix(line, "error "):
			t.Fatalf("the peer: %s", line)
		}
	}
	t.Fatalf("the peer ended: %v", p.out.Err())
	return nil
}

// TestMDNSPeerHelper is the peer's side of peerClient (MAKTABA_TEST_MDNS_PEER: its link).
func TestMDNSPeerHelper(t *testing.T) {
	ifname := os.Getenv("MAKTABA_TEST_MDNS_PEER")
	if ifname == "" {
		t.Skip("the peer's side of TestAnnounceLive")
	}
	ifi, err := net.InterfaceByName(ifname)
	if err != nil {
		fmt.Fprintf(os.Stdout, "error %v\n", err)
		return
	}
	c, err := newLiveClient(ifi)
	if err != nil {
		fmt.Fprintf(os.Stdout, "error %v\n", err)
		return
	}
	defer func() { _ = c.conn.Close() }()
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		qt, name, _ := strings.Cut(in.Text(), " ")
		qtype, _ := strconv.Atoi(qt)
		rrs, err := c.query(name, uint16(qtype))
		if err != nil {
			fmt.Fprintf(os.Stdout, "error %v\n", err)
			continue
		}
		for _, rr := range rrs {
			rr.Header().Class &^= mdnsCacheFlush // as NewRR reads it back
			fmt.Fprintf(os.Stdout, "rr %s\n", strings.ReplaceAll(rr.String(), "\n", " "))
		}
		fmt.Fprintln(os.Stdout, "end")
	}
}

// await asks until want holds of the answers, for up to 15 seconds.
func await(t *testing.T, c asker, what, name string, qtype uint16, want func([]dns.RR) bool) {
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
			t.Fatalf("%s: no; asked %s %s, heard:\n%s", what, dns.TypeToString[qtype], name, strings.Join(got, "\n"))
		}
	}
}

// resolvectlService asks resolved for the service (`resolvectl service`), a second client:
// resolved answers its own registrations from its zone.
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
