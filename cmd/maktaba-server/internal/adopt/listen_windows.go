//go:build windows

package adopt

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// tcpTableOwnerPIDListener is TCP_TABLE_OWNER_PID_LISTENER: the listening
// sockets, each with its process.
const tcpTableOwnerPIDListener = 3

// Listeners reads the TCP listener tables (IPv4 and IPv6) for port: what each
// socket is bound to and the process that holds it. The user needs no rights
// for it.
func (winSys) Listeners(port string) ([]Listener, error) {
	want, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("port %q", port)
	}
	var out []Listener
	for _, af := range []uint32{windows.AF_INET, windows.AF_INET6} {
		table, err := tcpListeners(af)
		if err != nil {
			return nil, fmt.Errorf("read the listening sockets: %w", err)
		}
		out = append(out, parseListeners(table, af, uint16(want))...)
	}
	return out, nil
}

// tcpListeners is one family's MIB_TCP(6)TABLE_OWNER_PID of listeners.
func tcpListeners(af uint32) ([]byte, error) {
	size := uint32(16 << 10)
	for range 5 {
		buf := make([]uint64, (size+7)/8) // the table's rows are 4-byte aligned
		r, _, _ := procGetExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(af), tcpTableOwnerPIDListener, 0)
		switch windows.Errno(r) {
		case 0:
			return unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), len(buf)*8)[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			continue // size is what it needs now
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}

// parseListeners reads the rows on port of a MIB_TCPTABLE_OWNER_PID (af
// AF_INET: state, local address, local port, remote address, remote port, pid)
// or a MIB_TCP6TABLE_OWNER_PID (AF_INET6: local address, scope, local port,
// remote address, scope, remote port, state, pid). A port is in network order
// in its field's first two bytes.
func parseListeners(table []byte, af uint32, port uint16) []Listener {
	if len(table) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(table))
	rowSize, addrAt, addrLen, portAt, pidAt := 24, 4, 4, 8, 20
	if af == windows.AF_INET6 {
		rowSize, addrAt, addrLen, portAt, pidAt = 56, 0, 16, 20, 52
	}
	var out []Listener
	for i := 0; i < n && 4+(i+1)*rowSize <= len(table); i++ {
		row := table[4+i*rowSize : 4+(i+1)*rowSize]
		if binary.BigEndian.Uint16(row[portAt:]) != port {
			continue
		}
		ip := net.IP(append([]byte(nil), row[addrAt:addrAt+addrLen]...))
		out = append(out, Listener{Addr: ip.String(), PID: int(binary.LittleEndian.Uint32(row[pidAt:]))})
	}
	return out
}
