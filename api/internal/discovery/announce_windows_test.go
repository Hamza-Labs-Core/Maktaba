//go:build windows

package discovery

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
// escaped name DnsServiceConstructInstance gets is the one Windows probes and announces.
// Windows answers no query of this machine's own, so what it sends is read off the
// network adapter with pktmon (elevated: the CI runner is).
func TestWindowsDNSAPIKeepsADottedNameOneLabel(t *testing.T) {
	dir := t.TempDir()
	etl := filepath.Join(dir, "mdns.etl")
	pktmon := func(args ...string) error {
		out, err := exec.Command("pktmon", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("pktmon %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return nil
	}
	_ = pktmon("stop")
	_ = pktmon("filter", "remove")
	if err := pktmon("filter", "add", "maktaba-mdns", "-t", "UDP", "-p", "5353"); err != nil {
		t.Skipf("no packet capture here (pktmon needs an elevated Windows): %v", err)
	}
	defer func() { _ = pktmon("filter", "remove") }()
	if err := pktmon("start", "--capture", "--pkt-size", "0", "--file-name", etl); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			if err := pktmon("stop"); err != nil {
				t.Error(err)
			}
		}
	}
	defer stop()

	const service = "_maktaba-dot._tcp"
	name := "Maktaba v1.2 a\\b مكتبة " + time.Now().Format("150405.000000")
	answered := make(chan error, 1)
	svc := winService{
		instance: escapeInstance(name) + "." + service + ".local",
		host:     announcedHost(systemResponder()) + ".local",
		port:     18087,
		keys:     []string{"role"},
		values:   []string{"home"},
	}
	svc.ip4, svc.ip6 = firstIPs(interfaceIPs())
	reg, err := windowsDNSAPI{}.register(svc, func(err error) { answered <- err })
	if err != nil {
		t.Fatalf("DnsServiceRegister: %v", err)
	}
	defer reg.deregister()
	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("registration refused: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no answer to the registration")
	}
	time.Sleep(3 * time.Second) // the announcements follow the probes
	stop()

	capture, err := os.ReadFile(etl)
	if err != nil {
		t.Fatal(err)
	}
	// and as pcapng, whose packets are their bytes as they went
	pcap := filepath.Join(dir, "mdns.pcapng")
	if err := pktmon("etl2pcap", etl, "--out", pcap); err != nil {
		t.Log(err)
	} else if b, err := os.ReadFile(pcap); err == nil {
		capture = append(capture, b...)
	}
	// one label: its length, the name's bytes, then the service's label or a pointer to it
	label := append([]byte{byte(len(name))}, name...)
	for rest := capture; ; {
		i := bytes.Index(rest, label)
		if i < 0 {
			break
		}
		if next := rest[i+len(label):]; len(next) > 0 && (next[0] == byte(len("_maktaba-dot")) || next[0] >= 0xC0) {
			return
		}
		rest = rest[i+1:]
	}
	var seen []string
	for rest := capture; len(seen) < 8; {
		i := bytes.Index(rest, []byte("Maktaba v1"))
		if i < 0 {
			break
		}
		from := max(i-1, 0)
		seen = append(seen, fmt.Sprintf("%q", rest[from:min(from+len(label)+16, len(rest))]))
		rest = rest[i+1:]
	}
	t.Fatalf("%d bytes captured; %q is not one label in them; it is in: %v", len(capture), name, seen)
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
