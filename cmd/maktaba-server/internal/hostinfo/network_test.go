package hostinfo

import "testing"

const routeTable = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
	"eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
	"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"

// Epic 59 story 59.08 (T04): the default route's interface tells a Docker bridge (veth)
// from the host's network and from macvlan; MAKTABA_NETWORK overrides; outside a
// container there is no reading.
func TestReadNetwork(t *testing.T) {
	t.Parallel()
	box := map[string]string{"/.dockerenv": "", "/proc/net/route": routeTable}
	cases := []struct {
		name   string
		files  map[string]string
		env    map[string]string
		driver string
		want   Network
	}{
		{"bridge", box, nil, "veth", Network{Mode: NetworkBridge, From: "detected", Interface: "eth0", Driver: "veth"}},
		{"host", box, nil, "igc", Network{Mode: NetworkHost, From: "detected", Interface: "eth0", Driver: "igc"}},
		{"macvlan", box, nil, "macvlan", Network{Mode: NetworkMacvlan, From: "detected", Interface: "eth0", Driver: "macvlan"}},
		{"host, a Linux bridge (Unraid br0)", box, nil, "bridge", Network{Mode: NetworkHost, From: "detected", Interface: "eth0", Driver: "bridge"}},
		{"no default route", map[string]string{"/.dockerenv": ""}, nil, "veth", Network{Mode: NetworkHost, From: "detected"}},
		{"env", box, map[string]string{EnvNetwork: "Bridge"}, "igc", Network{Mode: NetworkBridge, From: "env"}},
		{"MAKTABA_CONTAINER", map[string]string{"/proc/net/route": routeTable}, map[string]string{"MAKTABA_CONTAINER": "docker"}, "veth",
			Network{Mode: NetworkBridge, From: "detected", Interface: "eth0", Driver: "veth"}},
		{"not a container", map[string]string{"/proc/net/route": routeTable}, nil, "veth", Network{}},
	}
	for _, c := range cases {
		p := Probe{Root: fixture(t, c.files), Env: func(k string) string { return c.env[k] },
			Driver: func(iface string) string {
				if iface != "eth0" {
					t.Errorf("%s: driver of %q asked", c.name, iface)
				}
				return c.driver
			}}
		if got := readNetwork(p); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
		if got := readNetwork(p).Bridge(); got != (c.want.Mode == NetworkBridge) {
			t.Errorf("%s: Bridge() %v", c.name, got)
		}
	}
}
