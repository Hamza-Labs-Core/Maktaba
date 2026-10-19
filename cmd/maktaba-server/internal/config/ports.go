package config

// Port reconciliation (setup --node, setup --self-managed, the setup wizard and
// serve): the web, API and streaming binds of server.toml are checked before
// anything is started. A port another program holds (Splunk's 8088, MiniTool
// ShadowMaker's 8080 on Windows desktops) moves to the first free port of the
// fallback range and the new address is written to server.toml; nothing fails
// to bind silently.
//
// Fallback range: FallbackPortLow..FallbackPortHigh (8780-8790). The web port
// tries 8780 first, the streaming port 8781, the API 8782, each then the rest of
// the range in order. The range stays clear of the TV slot ports (8791-8793) and
// the intra-host role ports (9050, 9090, 9100-9113).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// FallbackPortLow and FallbackPortHigh bound the ports a taken bind moves to.
	FallbackPortLow  = 8780
	FallbackPortHigh = 8790
	// WindowsNodeWebPort is the web port a Windows full node prefers: 8088 is
	// Splunk's HTTP Event Collector on many Windows desktops.
	WindowsNodeWebPort = 8780
)

// PortChange is one bind moved by ReconcilePorts.
type PortChange struct {
	Key      string // server.toml key: web_listen, listen, stream_addr
	From, To string // the old and the new address
	Reason   string // why it moved
}

func (p PortChange) String() string {
	return fmt.Sprintf("%s %s -> %s (%s)", p.Key, p.From, p.To, p.Reason)
}

// PortProbe answers whether an address can be bound and whether a Maktaba
// already answers on a port of this host. Tests replace both.
type PortProbe struct {
	Free func(addr string) bool
	Ours func(port int) bool
}

// DefaultPortProbe binds for real and asks 127.0.0.1 over HTTP.
func DefaultPortProbe() PortProbe { return PortProbe{Free: CanBind, Ours: MaktabaAnswers} }

// PortOptions tune ReconcilePorts.
type PortOptions struct {
	// PreferWeb, when non-zero and web_listen is still the default port (8088),
	// moves the web port there when it is free (WindowsNodeWebPort on a Windows
	// full node).
	PreferWeb int
	// KeepOurs keeps a port a running Maktaba answers on (setup converting a
	// running install: that process is replaced, its port stays). Without it such
	// a port is an error (serve: another maktaba-server is already running).
	KeepOurs bool
	// Wait retries a taken port for this long before moving it (serve: the
	// previous instance may still be exiting).
	Wait time.Duration
	// Keys limits the check to these server.toml keys (serve --role); empty is all.
	Keys []string
}

// ErrPortOurs: a port is held by another running Maktaba.
type ErrPortOurs struct {
	Key, Addr string
}

func (e ErrPortOurs) Error() string {
	return fmt.Sprintf("%s %s is in use by another running Maktaba (is maktaba-server serve already running?)", e.Key, e.Addr)
}

type bind struct {
	key       string
	ptr       *string
	defPort   int
	firstFall int
}

// ReconcilePorts checks the web, streaming and API binds (in that order) and
// moves each one another program holds to a free port of the fallback range,
// updating c (and public_url / web_public_url when they name the old port). It
// returns what moved; the caller saves c and logs the changes.
func (c *Config) ReconcilePorts(p PortProbe, o PortOptions) ([]PortChange, error) {
	if p.Free == nil {
		p.Free = CanBind
	}
	if p.Ours == nil {
		p.Ours = func(int) bool { return false }
	}
	binds := []bind{
		{"web_listen", &c.Server.WebListen, 8088, FallbackPortLow},
		{"stream_addr", &c.Server.StreamAddr, 8081, FallbackPortLow + 1},
		{"listen", &c.Server.Listen, 8080, FallbackPortLow + 2},
	}
	used := map[int]bool{}
	for _, b := range binds {
		used[portOf(*b.ptr, b.defPort)] = true
	}
	var changes []PortChange
	for _, b := range binds {
		if !wantKey(o.Keys, b.key) {
			continue
		}
		addr := strings.TrimSpace(*b.ptr)
		host, port := splitBind(addr, b.defPort)

		if b.key == "web_listen" && o.PreferWeb > 0 && port == 8088 && o.PreferWeb != port &&
			!used[o.PreferWeb] && p.Free(joinBind(host, o.PreferWeb)) {
			to := joinBind(host, o.PreferWeb)
			changes = append(changes, c.move(b, addr, to, port, o.PreferWeb, "preferred web port on this platform"))
			delete(used, port)
			used[o.PreferWeb] = true
			continue
		}
		if p.Free(joinBind(host, port)) {
			continue
		}
		if waitFree(p, joinBind(host, port), o.Wait) {
			continue
		}
		if p.Ours(port) {
			if o.KeepOurs {
				continue
			}
			return changes, ErrPortOurs{Key: b.key, Addr: addr}
		}
		next := 0
		for _, cand := range fallbackOrder(b.firstFall) {
			if used[cand] {
				continue
			}
			if p.Free(joinBind(host, cand)) {
				next = cand
				break
			}
		}
		if next == 0 {
			return changes, fmt.Errorf("%s %s is in use by another program and no port of %d-%d is free: set %s in server.toml",
				b.key, addr, FallbackPortLow, FallbackPortHigh, b.key)
		}
		changes = append(changes, c.move(b, addr, joinBind(host, next), port, next,
			fmt.Sprintf("port %d is in use by another program", port)))
		delete(used, port)
		used[next] = true
	}
	return changes, nil
}

func (c *Config) move(b bind, from, to string, oldPort, newPort int, reason string) PortChange {
	*b.ptr = to
	switch b.key {
	case "stream_addr":
		c.Server.PublicURL = repointURL(c.Server.PublicURL, oldPort, newPort)
	case "web_listen":
		c.Server.WebPublicURL = repointURL(c.Server.WebPublicURL, oldPort, newPort)
	}
	return PortChange{Key: b.key, From: from, To: to, Reason: reason}
}

// repointURL replaces the port of u when it is oldPort (a public URL that named
// the moved bind); anything else is kept.
func repointURL(u string, oldPort, newPort int) string {
	s := strings.TrimSpace(u)
	if s == "" {
		return u
	}
	withScheme := s
	if !strings.Contains(withScheme, "://") {
		withScheme = "http://" + withScheme
	}
	pu, err := url.Parse(withScheme)
	if err != nil || pu.Port() != strconv.Itoa(oldPort) {
		return u
	}
	pu.Host = net.JoinHostPort(pu.Hostname(), strconv.Itoa(newPort))
	out := pu.String()
	if !strings.Contains(s, "://") {
		out = strings.TrimPrefix(out, "http://")
	}
	return out
}

func wantKey(keys []string, k string) bool {
	if len(keys) == 0 {
		return true
	}
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

func waitFree(p PortProbe, addr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if p.Free(addr) {
			return true
		}
	}
	return false
}

// fallbackOrder: first, then the rest of the range in ascending order.
func fallbackOrder(first int) []int {
	out := []int{first}
	for p := FallbackPortLow; p <= FallbackPortHigh; p++ {
		if p != first {
			out = append(out, p)
		}
	}
	return out
}

func splitBind(addr string, def int) (string, int) {
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		return "", def
	}
	n, err := strconv.Atoi(ps)
	if err != nil || n <= 0 || n > 65535 {
		return host, def
	}
	return host, n
}

func portOf(addr string, def int) int {
	_, p := splitBind(strings.TrimSpace(addr), def)
	return p
}

func joinBind(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

// CanBind reports whether addr can be listened on, and, for a wildcard bind,
// whether 127.0.0.1 on the same port is free too (on Windows a program on
// 127.0.0.1:port does not stop a wildcard bind but takes the local browser's
// requests).
func CanBind(addr string) bool {
	if !tryListen(addr) {
		return false
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		return tryListen(net.JoinHostPort("127.0.0.1", port))
	}
	return true
}

func tryListen(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// MaktabaAnswers reports whether a Maktaba service answers on 127.0.0.1:port:
// the api's or streaming's /healthz ({"status":"ok"}) or the web port's
// /api/system/version (carries schema_revision).
func MaktabaAnswers(port int) bool {
	base := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if m := getJSON(base + "/healthz"); m != nil && m["status"] == "ok" {
		return true
	}
	if m := getJSON(base + "/api/system/version"); m != nil {
		_, ok := m["schema_revision"]
		return ok
	}
	return false
}

func getJSON(u string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var m map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m) != nil {
		return nil
	}
	return m
}
