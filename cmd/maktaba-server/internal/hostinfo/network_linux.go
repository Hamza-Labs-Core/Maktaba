//go:build linux

package hostinfo

import "golang.org/x/sys/unix"

// ifaceDriver is an interface's driver as ethtool names it ("veth", "macvlan", "e1000e"),
// "" when it cannot be asked (ETHTOOL_GDRVINFO needs no privilege).
func ifaceDriver(name string) string {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return ""
	}
	defer func() { _ = unix.Close(fd) }()
	info, err := unix.IoctlGetEthtoolDrvinfo(fd, name)
	if err != nil {
		return ""
	}
	return unix.ByteSliceToString(info.Driver[:])
}
