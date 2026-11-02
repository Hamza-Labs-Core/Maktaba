package mdns

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
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
		dnsapi: func() bool { return dnsapi },
	}
}

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
