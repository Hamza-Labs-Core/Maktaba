package fakegateway

import (
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
)

// IPTables opens a port by a DNAT rule from the router's WAN address to the LAN host.
type IPTables struct {
	WAN netip.Addr
}

func (t IPTables) rule(op, proto string, external int, to netip.AddrPort) []string {
	return []string{"-t", "nat", op, "PREROUTING", "-d", t.WAN.String(), "-p", proto,
		"--dport", strconv.Itoa(external), "-j", "DNAT", "--to-destination", to.String()}
}

func (t IPTables) run(args []string) error {
	if out, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %v: %v: %s", args, err, out)
	}
	return nil
}

func (t IPTables) Open(proto string, external int, to netip.AddrPort) error {
	return t.run(t.rule("-A", proto, external, to))
}

func (t IPTables) Close(proto string, external int, to netip.AddrPort) error {
	return t.run(t.rule("-D", proto, external, to))
}

// Masquerade NATs the LAN's traffic out of the router's other interfaces.
func Masquerade(lan netip.Prefix) error {
	return IPTables{}.run([]string{"-t", "nat", "-A", "POSTROUTING", "-s", lan.String(), "!", "-d", lan.String(), "-j", "MASQUERADE"})
}
