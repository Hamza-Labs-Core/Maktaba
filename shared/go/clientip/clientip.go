// Package clientip resolves the address a request came from, believing a forwarded
// client address only from a peer that is trusted to write it (Epic 50 story 50.31 for the
// cloud, Epic 59 story 59.08 for the home).
//
// A forwarding header (CF-Connecting-IP, X-Forwarded-For, X-Real-IP) is text the client
// can write. Believed from any peer, it lets a client pick its own rate-limit key, audit
// address or "this machine" check. So each resolver here reads a header only when the TCP
// peer is in a trusted list, and otherwise answers the peer itself.
//
//   - Cloudflare is the cloud's: CF-Connecting-IP from a peer in server.trusted_proxies
//     (the local cloudflared, or Caddy after its origin checks).
//   - Forwarded is the home's: X-Forwarded-For (else X-Real-IP) from a loopback peer (this
//     machine's own proxies: the web port's, the relay agent, direct access) or one in the
//     home's [server] trusted_proxies (a reverse proxy in front, a NAS's for example).
//     Middleware rewrites r.RemoteAddr to it, as chi's RealIP did, and keeps the
//     connection's own address for Peer.
//   - Attested is a client an approved node vouched for with its delegation key (the
//     home's api checks that signature and puts the client here, WithAttested): Middleware
//     prefers it to any header. A node's address is never trusted for what it forwards.
package clientip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// EnvTrustedProxies is the environment variable the supervisor hands the home's api and
// streaming roles [server] trusted_proxies in, comma-separated.
const EnvTrustedProxies = "MAKTABA_TRUSTED_PROXIES"

// ParseCIDRs reads CIDRs only; a bare address is refused, so a list says what it means (the
// cloud's server.trusted_proxies).
func ParseCIDRs(vs []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(vs))
	for _, v := range vs {
		p, err := netip.ParsePrefix(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("%q is not a CIDR", v)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// ParseList reads addresses or CIDRs: a bare address is that one address (/32, or /128 for
// IPv6), an IPv4-mapped IPv6 address its IPv4 one. Blank entries are skipped. The error
// names the first entry that is neither, or that is a /0 (ParseEntry), by its index.
func ParseList(vs []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(vs))
	for i, v := range vs {
		p, err := ParseEntry(v)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if p.IsValid() {
			out = append(out, p)
		}
	}
	return out, nil
}

// ParseEntry reads one address or CIDR (ParseList); a blank entry is the zero Prefix. A /0
// (0.0.0.0/0, ::/0) is refused: it would believe every peer's forwarding header, which is
// the same as believing none of them is a lie.
func ParseEntry(v string) (netip.Prefix, error) {
	p, err := parseEntry(v)
	if err == nil && p.IsValid() && p.Bits() == 0 {
		return netip.Prefix{}, fmt.Errorf("%q %w", strings.TrimSpace(v), ErrEveryAddress)
	}
	return p, err
}

// ErrEveryAddress is ParseEntry's refusal of a /0.
var ErrEveryAddress = errors.New("trusts every address; name the proxy's own address instead")

func parseEntry(v string) (netip.Prefix, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return netip.Prefix{}, nil
	}
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil || p.Addr().Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%q is not an address or a CIDR", v)
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or a CIDR", v)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// SplitList splits a comma-separated list (EnvTrustedProxies), skipping blanks.
func SplitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// FromEnv reads EnvTrustedProxies (ParseList); unset or empty is no list.
func FromEnv() ([]netip.Prefix, error) {
	ps, err := ParseList(SplitList(os.Getenv(EnvTrustedProxies)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvTrustedProxies, err)
	}
	return ps, nil
}

// PeerAddr is the address in a RemoteAddr ("host:port", or a bare address), its zone
// dropped and an IPv4-mapped address unmapped; the zero Addr when it is neither.
func PeerAddr(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return ip.WithZone("").Unmap()
}

// Contains reports whether ip is in one of prefixes.
func Contains(prefixes []netip.Prefix, ip netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func addrString(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	return ip.String()
}

// Cloudflare is the cloud's client address (Epic 50 story 50.31): the TCP peer, port
// stripped, unless that peer is in trusted: then CF-Connecting-IP wins when it holds exactly
// one valid IP. X-Forwarded-For is never read here (the relay reads the one Caddy appends).
func Cloudflare(r *http.Request, trusted []netip.Prefix) string {
	peer := PeerAddr(r.RemoteAddr)
	v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))
	if v == "" || !peer.IsValid() {
		return addrString(peer)
	}
	if !Contains(trusted, peer) {
		cfIgnored.note("CF-Connecting-IP from an untrusted peer ignored", peer)
		return addrString(peer)
	}
	ip, err := netip.ParseAddr(v)
	if err != nil || ip.Zone() != "" {
		return addrString(peer)
	}
	return ip.Unmap().String()
}

// trustedHop reports whether the home believes what ip forwards: loopback always (this
// machine's own proxies), else the configured list.
func trustedHop(trusted []netip.Prefix, ip netip.Addr) bool {
	return ip.IsLoopback() || Contains(trusted, ip)
}

// Forwarded is the home's client address and whether a forwarding header gave it. From a
// trusted peer (loopback, or one in trusted) X-Forwarded-For is walked right to left, each
// trusted hop skipped: the first address that is not trusted is the client's, and when every
// hop is trusted, the leftmost. An entry that is not an address stops the walk at the hop
// that wrote it. With no X-Forwarded-For, a single valid X-Real-IP. From any other peer the
// headers are ignored and the answer is the peer.
func Forwarded(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	peer := PeerAddr(r.RemoteAddr)
	if !peer.IsValid() {
		return peer, false
	}
	xff := r.Header.Values("X-Forwarded-For")
	realIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if len(xff) == 0 && realIP == "" {
		return peer, false
	}
	if !trustedHop(trusted, peer) {
		fwdIgnored.note("X-Forwarded-For from an untrusted peer ignored", peer)
		return peer, false
	}
	if len(xff) > 0 {
		var hops []string
		for _, v := range xff {
			hops = append(hops, strings.Split(v, ",")...)
		}
		last, used := peer, false
		for i := len(hops) - 1; i >= 0; i-- {
			ip, ok := hopAddr(hops[i])
			if !ok {
				break
			}
			last, used = ip, true
			if !trustedHop(trusted, ip) {
				break
			}
		}
		return last, used
	}
	if ip, ok := hopAddr(realIP); ok {
		return ip, true
	}
	return peer, false
}

// hopAddr reads one forwarded address: a bare address, or one with a port ("[v6]:port",
// "v4:port"). A link-local IPv6 address's zone (fe80::1%eth0) is dropped: it names the
// proxy's interface, not the client, and kept it would refuse the hop and fold every such
// client onto the proxy's address.
func hopAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.WithZone("").Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}

// Client is Forwarded's address as a string ("" for a RemoteAddr that is no address).
func Client(r *http.Request, trusted []netip.Prefix) string {
	ip, _ := Forwarded(r, trusted)
	return addrString(ip)
}

type peerKey struct{}

type attestedKey struct{}

// WithAttested marks ctx with a client a verified party vouched for: the home's api puts
// here the client an approved node signed with its delegation key, or sent over its
// authenticated cluster link. Only that check may call it; an invalid ip is ignored.
func WithAttested(ctx context.Context, ip netip.Addr) context.Context {
	if !ip.IsValid() {
		return ctx
	}
	return context.WithValue(ctx, attestedKey{}, ip.WithZone("").Unmap())
}

// Attested is the client WithAttested put in ctx (false: none).
func Attested(ctx context.Context) (netip.Addr, bool) {
	ip, ok := ctx.Value(attestedKey{}).(netip.Addr)
	return ip, ok && ip.IsValid()
}

// Middleware resolves each request's client and, when one other than the peer is known,
// rewrites r.RemoteAddr to that bare address, as chi's RealIP did; everything after it
// (the logs, the rate limits) reads the client. An attested client (WithAttested) wins;
// otherwise the forwarding headers of a trusted peer (Forwarded). The connection's own
// address stays readable through Peer, for a check on this machine's own callers.
func Middleware(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, used := Attested(r.Context())
			if !used {
				ip, used = Forwarded(r, trusted)
			}
			if used {
				peer := r.RemoteAddr
				r = r.WithContext(context.WithValue(r.Context(), peerKey{}, peer))
				r.RemoteAddr = ip.String()
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Peer is the connection's own address ("host:port"): the RemoteAddr before Middleware
// rewrote it, else RemoteAddr.
func Peer(r *http.Request) string {
	if v, ok := r.Context().Value(peerKey{}).(string); ok {
		return v
	}
	return r.RemoteAddr
}

// Host is the address in a RemoteAddr without its port ("" for none).
func Host(remoteAddr string) string {
	return addrString(PeerAddr(remoteAddr))
}

// throttle keeps a debug line for an ignored header to one a minute.
type throttle struct{ last atomic.Int64 }

var cfIgnored, fwdIgnored throttle

func (t *throttle) note(msg string, peer netip.Addr) {
	now := time.Now().Unix()
	last := t.last.Load()
	if now-last < 60 || !t.last.CompareAndSwap(last, now) {
		return
	}
	slog.Debug(msg, "peer", peer.String())
}
