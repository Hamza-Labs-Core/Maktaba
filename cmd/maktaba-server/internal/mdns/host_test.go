package mdns

import (
	"context"
	"net"
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

// A macOS host name already ends in ".local"; the announced name must not carry it
// (zeroconf appends ".local." itself).
func TestBareHost(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Mahmouds-Mac-Studio.local":  "Mahmouds-Mac-Studio",
		"Mahmouds-Mac-Studio.local.": "Mahmouds-Mac-Studio",
		"Studio.LOCAL":               "Studio",
		"studio":                     "studio",
		"studio.lan":                 "studio",
		" studio.local \n":           "studio",
		"":                           "maktaba",
		".":                          "maktaba",
	}
	for in, want := range cases {
		if got := bareHost(in); got != want {
			t.Errorf("bareHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnnouncedIPs(t *testing.T) {
	t.Parallel()
	nets := func(ips ...string) []net.Addr {
		var out []net.Addr
		for _, s := range ips {
			out = append(out, &net.IPNet{IP: net.ParseIP(s), Mask: net.CIDRMask(64, 128)})
		}
		return out
	}
	if got := announcedIPs(nets("127.0.0.1", "192.168.1.20", "fe80::1", "2001:db8::5")); !reflect.DeepEqual(got, []string{"192.168.1.20", "2001:db8::5"}) {
		t.Errorf("with a global IPv6: %v", got)
	}
	if got := announcedIPs(nets("10.0.0.2", "fe80::1", "::1")); !reflect.DeepEqual(got, []string{"10.0.0.2", "fe80::1"}) {
		t.Errorf("link-local only: %v", got)
	}
	if got := announcedIPs(nil); len(got) != 0 {
		t.Errorf("no addresses: %v", got)
	}
}

// The real announcement, resolved over multicast: its target is "<host>.local.", never
// "<host>.local.local.". Skipped where the machine cannot multicast (some CI runners).
func TestRegisterAnnouncesBareHost(t *testing.T) {
	if testing.Short() {
		t.Skip("multicast")
	}
	instance := "Maktaba test " + time.Now().Format("150405.000000")
	srv, err := register(instance, "_maktaba-test._tcp", "local.", 18088, []string{"web=8088"})
	if err != nil {
		t.Skipf("no multicast here: %v", err)
	}
	defer srv.Shutdown()

	res, err := zeroconf.NewResolver(nil)
	if err != nil {
		t.Skipf("no multicast here: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 4)
	if err := res.Lookup(ctx, instance, "_maktaba-test._tcp", "local.", entries); err != nil {
		t.Skipf("no multicast here: %v", err)
	}
	select {
	case e := <-entries:
		h, _ := os.Hostname()
		if want := bareHost(h) + ".local."; e.HostName != want {
			t.Fatalf("target = %q, want %q", e.HostName, want)
		}
		if e.Port != 18088 || !reflect.DeepEqual(e.Text, []string{"web=8088"}) {
			t.Fatalf("port %d txt %v", e.Port, e.Text)
		}
	case <-ctx.Done():
		t.Skip("the announcement was not heard (multicast blocked)")
	}
}

// On a Mac the announced host is the Bonjour LocalHostName, not os.Hostname (which
// follows the network and named a host that resolved to nothing).
func TestHostNameIsTheMacLocalHostName(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	was := macLocalHostName
	t.Cleanup(func() { macLocalHostName = was })
	macLocalHostName = func() string { return "Sara-Mac-mini" }
	if got := bareHost(hostName()); got != "Sara-Mac-mini" {
		t.Fatalf("host = %q, want the LocalHostName", got)
	}
	macLocalHostName = func() string { return "" } // unreadable: os.Hostname
	if h, _ := os.Hostname(); hostName() != h {
		t.Fatalf("host = %q, want os.Hostname %q", hostName(), h)
	}
}
