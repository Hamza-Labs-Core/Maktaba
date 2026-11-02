package main

import (
	"net"
	"testing"
)

// The module's go.mod sets `godebug multipathtcp=0`: no listener of this binary uses
// multipath TCP, the path into CVE-2026-98432 (docs/security/advisories). This fails if the line is lost.
func TestListenersUseNoMultipathTCP(t *testing.T) {
	var lc net.ListenConfig
	if lc.MultipathTCP() {
		t.Fatal("listeners default to multipath TCP: go.mod needs `godebug multipathtcp=0`")
	}
}
