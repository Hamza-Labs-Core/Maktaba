// Package ports is the one list of the ports Maktaba listens on: every default
// port a Maktaba process binds is declared here once, with the process that owns
// it, what it is for, and whether it is reached from this machine only (loopback)
// or from the network (public). The ports of a program Maktaba keeps off (a
// user's own Postgres, Ollama) are listed too, so nothing of Maktaba's takes them.
//
// Every caller takes its default from here (the supervisor's children, the api,
// streaming), and the tests fail when two entries share a port, every port of
// every range included. Before this list the ports were literals spread over the
// modules, the same formula written twice (the TV slots' 9111+i in the supervisor
// and in streaming), and on the owner's Mac the transcription workers (9110+i)
// took the TV slots' admin ports: the slots crash-looped for days on "bind:
// address already in use", silently, and every update was refused.
//
// server.toml's [server] addresses still win over these defaults; Clashes checks
// the addresses in effect among themselves at serve's start.
package ports

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// The hosts an entry binds by default.
const (
	// Loopback: this machine only.
	Loopback = "127.0.0.1"
	// AllIPv4: every IPv4 interface (the network reaches it).
	AllIPv4 = "0.0.0.0"
	// AllInterfaces: every interface, IPv4 and IPv6 (":8081").
	AllInterfaces = ""
)

// Scope says who reaches a port.
type Scope string

const (
	// ScopeLoopback: this machine only.
	ScopeLoopback Scope = "loopback"
	// ScopePublic: the network too.
	ScopePublic Scope = "public"
)

// Proto is the transport an entry listens on.
type Proto string

const (
	TCP Proto = "tcp"
	UDP Proto = "udp"
)

// Entry is one port, or a range of Count ports from Port, and who uses it.
type Entry struct {
	// Name is what messages call it ("api admin").
	Name string
	// Owner is the process that binds it: a supervisor role ("api", "tv-slot"),
	// "serve" (maktaba-server itself), or the other program a Foreign port is.
	Owner string
	// Purpose says what it serves.
	Purpose string
	// Port is the port, or a range's first port.
	Port int
	// Count is how many ports the range has (0 and 1: a single port).
	Count int
	// Host is the default bind host: Loopback, AllIPv4 or AllInterfaces.
	Host  string
	Proto Proto
	// Foreign: another program's port that Maktaba stays off; it binds nothing here.
	Foreign bool
}

// Len is the number of ports the entry covers.
func (e Entry) Len() int {
	if e.Count < 1 {
		return 1
	}
	return e.Count
}

// Last is the entry's last port (Port for a single port).
func (e Entry) Last() int { return e.Port + e.Len() - 1 }

// At is the range's i-th port, from 0. An i outside the range is a caller's bug;
// the port is still Port+i, as the formulas this replaced gave.
func (e Entry) At(i int) int { return e.Port + i }

// Contains reports whether port is one of the entry's.
func (e Entry) Contains(port int) bool { return port >= e.Port && port <= e.Last() }

// Scope is ScopeLoopback for a loopback host, else ScopePublic.
func (e Entry) Scope() Scope {
	if e.Host == Loopback {
		return ScopeLoopback
	}
	return ScopePublic
}

// PortString is Port as text ("8080"), the default port of a bind spec.
func (e Entry) PortString() string { return strconv.Itoa(e.Port) }

// Addr is the default bind address: Host and Port ("127.0.0.1:9100", ":8081").
func (e Entry) Addr() string { return e.AddrAt(0) }

// AddrAt is the range's i-th port's bind address.
func (e Entry) AddrAt(i int) string { return net.JoinHostPort(e.Host, strconv.Itoa(e.At(i))) }

// Local is the address on this machine a client dials ("127.0.0.1:9100").
func (e Entry) Local() string { return e.LocalAt(0) }

// LocalAt is the range's i-th port on this machine.
func (e Entry) LocalAt(i int) string { return net.JoinHostPort(Loopback, strconv.Itoa(e.At(i))) }

// Every returns Port on every interface (":9101"), what the standalone binaries
// bind when nothing says otherwise.
func (e Entry) Every() string { return ":" + e.PortString() }

// String names the entry and its ports: "tv-slot admin (9111-9113)".
func (e Entry) String() string {
	if e.Len() > 1 {
		return fmt.Sprintf("%s (%d-%d)", e.Name, e.Port, e.Last())
	}
	return fmt.Sprintf("%s (%d)", e.Name, e.Port)
}

// Lookup is the entry port belongs to and its index in the entry's range.
func Lookup(port int) (Entry, int, bool) {
	for _, e := range registry {
		if e.Contains(port) {
			return e, port - e.Port, true
		}
	}
	return Entry{}, 0, false
}

// All is every entry, in the order declared.
func All() []Entry { return append([]Entry(nil), registry...) }

// Overlap is two entries sharing at least one port.
type Overlap struct {
	A, B Entry
	Port int // the first port they share
}

func (o Overlap) String() string {
	return fmt.Sprintf("port %d is both %s's %s and %s's %s", o.Port, o.A.Owner, o.A, o.B.Owner, o.B)
}

// Overlaps is every pair of entries that share a port, every port of every range
// compared, whatever their protocol (strictly: nothing has needed two of them on
// one number).
func Overlaps(entries []Entry) []Overlap {
	var out []Overlap
	for i, a := range entries {
		for _, b := range entries[i+1:] {
			if lo := max(a.Port, b.Port); lo <= min(a.Last(), b.Last()) {
				out = append(out, Overlap{A: a, B: b, Port: lo})
			}
		}
	}
	return out
}

// Bind is an address one process listens on at runtime: the default or what
// server.toml set.
type Bind struct {
	// Addr is the bind spec ("127.0.0.1:9111", ":8081").
	Addr string
	// Owner is the process ("tv-slot/blue", "api", "serve").
	Owner string
	// What it is to its owner ("admin port").
	What string
}

// Port is the bind's port, 0 when Addr has none.
func (b Bind) Port() int {
	_, p, err := net.SplitHostPort(strings.TrimSpace(b.Addr))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0
	}
	return n
}

// Host is the bind's host ("" for ":8081").
func (b Bind) Host() string {
	h, _, err := net.SplitHostPort(strings.TrimSpace(b.Addr))
	if err != nil {
		return ""
	}
	return h
}

func (b Bind) String() string { return fmt.Sprintf("%s's %s (%s)", b.Owner, b.What, b.Addr) }

// Clash is two binds that cannot both listen.
type Clash struct{ A, B Bind }

func (c Clash) String() string {
	return fmt.Sprintf("port %d is both %s and %s", c.A.Port(), c.A, c.B)
}

// Clashes is every pair of binds on one port whose hosts meet: the same host, or
// one of them every interface. 127.0.0.1:9090 and 192.168.1.5:9090 do not clash;
// :9090 and 127.0.0.1:9090 do. Binds with no port (0: the OS picks) never clash.
func Clashes(binds []Bind) []Clash {
	var out []Clash
	for i, a := range binds {
		pa := a.Port()
		if pa == 0 {
			continue
		}
		for _, b := range binds[i+1:] {
			if b.Port() == pa && hostsMeet(a.Host(), b.Host()) {
				out = append(out, Clash{A: a, B: b})
			}
		}
	}
	return out
}

func hostsMeet(a, b string) bool {
	a, b = canonHost(a), canonHost(b)
	return a == "" || b == "" || a == b
}

// canonHost is "" for every interface, "127.0.0.1" for this machine's loopback.
func canonHost(h string) string {
	switch strings.ToLower(strings.Trim(h, "[]")) {
	case "", "0.0.0.0", "::":
		return ""
	case "localhost", "127.0.0.1":
		return Loopback
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}
