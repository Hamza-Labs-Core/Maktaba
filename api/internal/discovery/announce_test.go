package discovery

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/miekg/dns"
)

// fakeEnv is a machine with the given commands on its PATH, the given files, and dnsapi
// or not.
func fakeEnv(goos string, commands map[string]string, files []string, dnsapi bool) responderEnv {
	return responderEnv{
		goos: goos,
		lookPath: func(name string) (string, error) {
			if p, ok := commands[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		exists: func(path string) bool {
			for _, f := range files {
				if f == path {
					return true
				}
			}
			return false
		},
		dnsapi:   func() bool { return dnsapi },
		resolved: func() resolvedState { return resolvedState{} },
	}
}

// withResolved is env with systemd-resolved as st.
func withResolved(env responderEnv, st resolvedState) responderEnv {
	env.resolved = func() resolvedState { return st }
	return env
}

// resolvedOK is a systemd-resolved that answers mDNS and takes this process's
// registrations.
var resolvedOK = resolvedState{responds: true, canRegister: true, host: "studio"}

// Each system announces through its own responder where it has one; zeroconf answers
// alone only where nothing else does.
func TestSystemResponderPerOS(t *testing.T) {
	t.Parallel()
	all := map[string]string{"dns-sd": "/usr/bin/dns-sd", "avahi-publish-service": "/usr/bin/avahi-publish-service"}
	avahi := []string{"/run/avahi-daemon/socket"}
	cases := []struct {
		name string
		env  responderEnv
		want responder
	}{
		{"mac", fakeEnv("darwin", all, nil, false), responder{kind: viaDNSSD, path: "/usr/bin/dns-sd"}},
		{"mac without dns-sd", fakeEnv("darwin", nil, nil, false), responder{kind: viaZeroconf}},
		{"linux with avahi", fakeEnv("linux", all, avahi, false), responder{kind: viaAvahi, path: "/usr/bin/avahi-publish-service"}},
		{"linux with avahi (old socket path)", fakeEnv("linux", all, []string{"/var/run/avahi-daemon/socket"}, false), responder{kind: viaAvahi, path: "/usr/bin/avahi-publish-service"}},
		{"linux, avahi without avahi-utils", fakeEnv("linux", nil, avahi, false), responder{kind: viaZeroconfOwnHost}},
		{"linux, avahi-utils but no daemon", fakeEnv("linux", all, nil, false), responder{kind: viaZeroconf}},
		{"freebsd with avahi", fakeEnv("freebsd", all, avahi, false), responder{kind: viaAvahi, path: "/usr/bin/avahi-publish-service"}},
		{"windows", fakeEnv("windows", all, nil, true), responder{kind: viaDNSAPI}},
		{"windows without dnsapi", fakeEnv("windows", all, avahi, false), responder{kind: viaZeroconf}},
		{"linux, resolved answers mDNS", withResolved(fakeEnv("linux", all, nil, false), resolvedOK), responder{kind: viaResolved, host: "studio"}},
		{"linux, resolved answers but this process may not register", withResolved(fakeEnv("linux", all, nil, false), resolvedState{responds: true, host: "studio"}), responder{kind: viaZeroconfOwnHost}},
		{"linux, resolved answers for no name it says", withResolved(fakeEnv("linux", all, nil, false), resolvedState{responds: true, canRegister: true}), responder{kind: viaZeroconfOwnHost}},
		{"linux, resolved runs without mDNS", withResolved(fakeEnv("linux", all, nil, false), resolvedState{canRegister: true, host: "studio"}), responder{kind: viaZeroconf}},
		{"linux, avahi and resolved", withResolved(fakeEnv("linux", all, avahi, false), resolvedOK), responder{kind: viaAvahi, path: "/usr/bin/avahi-publish-service"}},
		{"freebsd never asks resolved", withResolved(fakeEnv("freebsd", all, nil, false), resolvedOK), responder{kind: viaZeroconf}},
		{"mac never asks resolved", withResolved(fakeEnv("darwin", nil, nil, false), resolvedOK), responder{kind: viaZeroconf}},
	}
	for _, c := range cases {
		if got := systemResponderFor(c.env); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// Beside an Avahi zeroconf cannot leave the host name to, it answers for a name of its
// own; everywhere else the target is the machine's own name.
func TestAnnouncedHost(t *testing.T) {
	t.Parallel()
	own := bareHost(hostName())
	for _, k := range []responderKind{viaZeroconf, viaDNSSD, viaAvahi, viaDNSAPI} {
		if got := announcedHost(responder{kind: k}); got != own {
			t.Errorf("kind %d: %q, want %q", k, got, own)
		}
	}
	got := announcedHost(responder{kind: viaZeroconfOwnHost})
	if got != dnsLabel(own, 63-len(ownHostSuffix))+"-maktaba" || len(got) > 63 {
		t.Errorf("beside avahi: %q", got)
	}
	// systemd-resolved's own name, which it answers for (it may differ from the host name)
	if got := announcedHost(responder{kind: viaResolved, host: "studio-2"}); got != "studio-2" {
		t.Errorf("through resolved: %q", got)
	}
	if got := announcedHost(responder{kind: viaResolved}); got != own {
		t.Errorf("through resolved, no name: %q", got)
	}
}

// Windows splits a whole name at every "." (no escape keeps one), so a home's "." is
// U+2024 there; a "\" and Arabic stay as they are.
func TestWindowsInstance(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Maktaba (studio)":      "Maktaba (studio)",
		"Maktaba (v1.2)":        "Maktaba (v1\u20242)",
		`Maktaba (a\b)`:         `Maktaba (a\b)`,
		`.lead\.trail.`:         "\u2024lead\\\u2024trail\u2024",
		"Maktaba (مكتبة البيت)": "Maktaba (مكتبة البيت)",
		"مكتبة 2.0":             "مكتبة 2\u20240",
	}
	for in, want := range cases {
		if got := windowsInstance(in); got != want {
			t.Errorf("windowsInstance(%q) = %q, want %q", in, got, want)
		}
	}
}

// zeroconf gets the instance as the DNS library writes a label: packed, it is one label
// holding the name's own bytes, and unpacked it reads back as what zeroconf was given (so
// a query naming the instance matches it).
func TestZeroconfInstanceIsOneLabel(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"Maktaba (studio)", "Maktaba (v1.2)", `Maktaba (a\b)`, "Maktaba (مكتبة البيت)",
		`Maktaba ("x"; @y 'z')`, ".lead", "trail.", "1.2.3",
	} {
		full := zeroconfInstance(name) + "._maktaba._tcp.local."
		m := new(dns.Msg)
		m.Answer = []dns.RR{&dns.PTR{Hdr: dns.RR_Header{Name: "_maktaba._tcp.local.", Rrtype: dns.TypePTR, Class: dns.ClassINET}, Ptr: full}}
		wire, err := m.Pack()
		if err != nil {
			t.Fatalf("%q: pack: %v", name, err)
		}
		label := append([]byte{byte(len(name))}, name...)
		if !bytes.Contains(wire, append(label, "\x08_maktaba"...)) {
			t.Errorf("%q: not one label on the wire (%q)", name, full)
		}
		back := new(dns.Msg)
		if err := back.Unpack(wire); err != nil {
			t.Fatalf("%q: unpack: %v", name, err)
		}
		if got := back.Answer[0].(*dns.PTR).Ptr; got != full && !strings.HasSuffix(name, ".") {
			t.Errorf("%q: reads back as %q, zeroconf has %q", name, got, full)
		}
	}
}

// A home name too long for DNS-SD, or with control characters, is fitted to it rather than
// refused (a refused name would be retried for ever).
func TestDNSLabelFitsDNSSD(t *testing.T) {
	t.Parallel()
	if got := dnsLabel("Maktaba (studio)\n\t", 63); got != "Maktaba (studio)" {
		t.Fatalf("control characters: %q", got)
	}
	long := "Maktaba (" + strings.Repeat("مكتبة", 10) + ")"
	got := dnsLabel(long, 63)
	if len(got) > 63 || !utf8.ValidString(got) {
		t.Fatalf("long name: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if !strings.HasPrefix(long, got) {
		t.Fatalf("not a prefix: %q", got)
	}
	if got := txtStrings([]string{strings.Repeat("a", 300)}); len(got[0]) != 255 {
		t.Fatalf("txt entry: %d bytes", len(got[0]))
	}
}

func TestTXTPairsAndFirstIPs(t *testing.T) {
	t.Parallel()
	keys, values := txtPairs([]string{"role=home", "flag", "url=http://x/?a=b", strings.Repeat("k", 300)})
	if !reflect.DeepEqual(keys, []string{"role", "flag", "url", strings.Repeat("k", 255)}) {
		t.Errorf("keys %q", keys)
	}
	if !reflect.DeepEqual(values, []string{"home", "", "http://x/?a=b", ""}) {
		t.Errorf("values %q", values)
	}
	ip4, ip6 := firstIPs([]string{"fe80::1", "192.168.1.20", "10.0.0.2", "2001:db8::5", "junk"})
	if ip4 != "192.168.1.20" || ip6 != "fe80::1" {
		t.Errorf("ips %q %q", ip4, ip6)
	}
	if ip4, ip6 := firstIPs(nil); ip4 != "" || ip6 != "" {
		t.Errorf("none: %q %q", ip4, ip6)
	}
}

// fakeDNSAPI is a dnsapi.dll: it records registrations and answers each with the next of
// answers (nil: taken) once one is given.
type fakeDNSAPI struct {
	mu      sync.Mutex
	regs    []*fakeReg
	refuse  int // the first refuse registrations are refused
	callErr error
}

type fakeReg struct {
	svc  winService
	mu   sync.Mutex
	gone bool
}

func (r *fakeReg) deregister() {
	r.mu.Lock()
	r.gone = true
	r.mu.Unlock()
}

func (r *fakeReg) withdrawn() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gone
}

func (f *fakeDNSAPI) register(svc winService, done func(error)) (winRegistration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.callErr != nil {
		return nil, f.callErr
	}
	r := &fakeReg{svc: svc}
	f.regs = append(f.regs, r)
	if f.refuse > 0 {
		f.refuse--
		done(errors.New("refused"))
	} else {
		done(nil)
	}
	return r, nil
}

// registrations waits up to a few seconds for at least n.
func (f *fakeDNSAPI) registrations(t *testing.T, n int) []*fakeReg {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		regs := append([]*fakeReg(nil), f.regs...)
		f.mu.Unlock()
		if len(regs) >= n {
			return regs
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %d registrations, have %d", n, len(regs))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Windows gets the full instance name, the host and its addresses, the TXT as keys and
// values; a new TXT withdraws that registration and makes one with it; Shutdown withdraws
// it, and nothing registers again.
func TestDNSAPIAnnouncesChangesAndWithdraws(t *testing.T) {
	api := &fakeDNSAPI{}
	a, err := startDNSAPI(api, "Maktaba (studio)\n", "_maktaba._tcp", "local.", "Studio.local", 8080,
		[]string{"fe80::1", "192.168.1.20"}, []string{"role=home", "web=8088"})
	if err != nil {
		t.Fatal(err)
	}
	first := api.registrations(t, 1)[0]
	want := winService{
		instance: "Maktaba (studio)._maktaba._tcp.local", host: "Studio.local",
		ip4: "192.168.1.20", ip6: "fe80::1", port: 8080,
		keys: []string{"role", "web"}, values: []string{"home", "8088"},
	}
	if !reflect.DeepEqual(first.svc, want) {
		t.Fatalf("registered %+v\nwant %+v", first.svc, want)
	}

	a.SetText([]string{"role=home", "web=8090"})
	second := api.registrations(t, 2)[1]
	if !first.withdrawn() || second.withdrawn() {
		t.Fatalf("after SetText: first withdrawn %v, second %v", first.withdrawn(), second.withdrawn())
	}
	if !reflect.DeepEqual(second.svc.values, []string{"home", "8090"}) || second.svc.instance != want.instance {
		t.Fatalf("after SetText: %+v", second.svc)
	}

	a.Shutdown()
	if !second.withdrawn() {
		t.Fatal("Shutdown left it registered")
	}
	a.SetText([]string{"role=home"})
	time.Sleep(50 * time.Millisecond)
	if n := len(api.registrations(t, 2)); n != 2 {
		t.Fatalf("registered again after Shutdown: %d", n)
	}

	// Windows takes the whole name: a "." of the instance's is not one there, a "\" is kept,
	// and the label, so lengthened, still fits
	b, err := startDNSAPI(api, `Maktaba (v1.2 a\b)`, "_maktaba._tcp", "local.", "Studio.local", 8080, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Shutdown()
	if got := api.registrations(t, 3)[2].svc.instance; got != "Maktaba (v1\u20242 a\\b)._maktaba._tcp.local" {
		t.Fatalf("dotted name: %q", got)
	}
	c, err := startDNSAPI(api, strings.Repeat(".", 40), "_maktaba._tcp", "local.", "Studio.local", 8080, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	if label, _, _ := strings.Cut(api.registrations(t, 4)[3].svc.instance, "._maktaba"); len(label) > 63 || label != strings.Repeat("\u2024", 21) {
		t.Fatalf("long dotted name: %q (%d bytes)", label, len(label))
	}
}

// A registration Windows refuses is made again, after a moment, until it is taken.
func TestDNSAPIRegistersAgainWhenRefused(t *testing.T) {
	was := dnssdRestart
	dnssdRestart = 20 * time.Millisecond
	t.Cleanup(func() { dnssdRestart = was })

	api := &fakeDNSAPI{refuse: 2}
	a, err := startDNSAPI(api, "Maktaba (studio)", "_maktaba._tcp", "local.", "Studio.local", 8080, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	regs := api.registrations(t, 3)
	time.Sleep(100 * time.Millisecond)
	if n := len(api.registrations(t, 3)); n != 3 {
		t.Fatalf("registered %d times; once taken it should stay", n)
	}
	if !regs[0].withdrawn() || !regs[1].withdrawn() || regs[2].withdrawn() {
		t.Fatal("refused registrations should be let go, the taken one kept")
	}
	a.Shutdown()
	if !regs[2].withdrawn() {
		t.Fatal("Shutdown left it registered")
	}
}

// One Windows does not take at all fails the announcement (the caller logs it).
func TestDNSAPIReportsARequestNotTaken(t *testing.T) {
	t.Parallel()
	api := &fakeDNSAPI{callErr: errors.New("no")}
	if _, err := startDNSAPI(api, "Maktaba", "_maktaba._tcp", "local.", "Studio.local", 8080, nil, nil); err == nil {
		t.Fatal("no error")
	}
}

// Shutdown while a refused registration waits to be made again: it is not made.
func TestDNSAPIShutdownStopsRetries(t *testing.T) {
	was := dnssdRestart
	dnssdRestart = 30 * time.Millisecond
	t.Cleanup(func() { dnssdRestart = was })

	api := &fakeDNSAPI{refuse: 100}
	a, err := startDNSAPI(api, "Maktaba", "_maktaba._tcp", "local.", "Studio.local", 8080, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	api.registrations(t, 1)
	a.Shutdown()
	time.Sleep(150 * time.Millisecond)
	if n := len(api.registrations(t, 1)); n > 2 {
		t.Fatalf("registered %d times after Shutdown", n)
	}
}
