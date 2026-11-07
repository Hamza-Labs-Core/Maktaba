package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
)

// Role identifies which set of features this process is providing.
type Role string

const (
	RoleAPI    Role = "api"
	RoleRelay  Role = "relay"
	RoleWorker Role = "worker"
	// RoleDNS is the direct-name domains' name server (Epic 50 story 50.15).
	RoleDNS Role = "dns"
)

// Validate applies role-specific required-field checks. The api role is
// the strictest: it owns identity, billing, and admin surfaces.
func Validate(c Config, role Role) error {
	if c.Database.URL == "" {
		return fmt.Errorf("%w: [database].url", ErrMissing)
	}
	switch role {
	case RoleAPI:
		if c.Server.ListenAddr == "" {
			return fmt.Errorf("%w: [server].listen_addr", ErrMissing)
		}
		if c.Mail.Driver != "log" {
			return fmt.Errorf("config: [mail].driver %q: the only driver is \"log\"", c.Mail.Driver)
		}
		if err := validateBilling(c.Billing); err != nil {
			return err
		}
		if err := validateOrigin(c.Server); err != nil {
			return err
		}
		// the auth pages' bot check (story 50.30 §4) is never on without its secret
		if c.Turnstile.Enabled && c.Turnstile.Secret == "" {
			return fmt.Errorf("%w: turnstile.enabled is on but MAKTABA_CLOUD_TURNSTILE_SECRET is not set", ErrMissing)
		}
	case RoleRelay:
		if c.Server.ListenAddr == "" {
			return fmt.Errorf("%w: [server].listen_addr", ErrMissing)
		}
		if c.Relay.PublicHost == "" {
			return fmt.Errorf("%w: [relay].public_host", ErrMissing)
		}
		if err := ValidatePeerAddr(c.Relay.PeerAddr); err != nil {
			return err
		}
	case RoleWorker:
		// Worker uses control port only; no public listener required.
	case RoleDNS:
		if len(c.Direct.Domains) == 0 {
			return fmt.Errorf("%w: [direct].domains (the dns role answers them)", ErrMissing)
		}
		for _, ns := range []struct{ key, v string }{{"ns1", c.Direct.NS1}, {"ns2", c.Direct.NS2}} {
			if net.ParseIP(ns.v) == nil {
				return fmt.Errorf("config: [direct].%s %q: the node's IP address", ns.key, ns.v)
			}
		}
	default:
		return fmt.Errorf("config: unknown role %q", role)
	}
	return nil
}

// BillingProviders lists the payment providers the api role accepts in
// [billing].provider. The provider is not chosen yet; "none" takes no
// payments. Keep in step with billing.New.
var BillingProviders = []string{"none"}

// ErrUnknownBillingProvider refuses a [billing].provider we do not ship.
var ErrUnknownBillingProvider = errors.New("config: unknown [billing].provider")

// validateBilling accepts only a provider we ship; empty means "none".
func validateBilling(b BillingConfig) error {
	if b.Provider == "" {
		return nil
	}
	for _, p := range BillingProviders {
		if b.Provider == p {
			return nil
		}
	}
	return fmt.Errorf("%w %q: the only provider is \"none\"", ErrUnknownBillingProvider, b.Provider)
}

// ErrPublicPeerAddr: [relay].peer_addr would listen where the internet can
// reach it. The peer port trusts a signed forward, so it binds only to
// loopback or a private address (story 50.04).
var ErrPublicPeerAddr = errors.New("config: [relay].peer_addr must be a loopback or private address")

// ValidatePeerAddr accepts "" (no peer listener) or host:port with a
// loopback or private IP host; never a wildcard or a public address.
func ValidatePeerAddr(addr string) error {
	if addr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: [relay].peer_addr %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || !ip.IsLoopback() && !ip.IsPrivate() {
		return fmt.Errorf("%w (got %q)", ErrPublicPeerAddr, addr)
	}
	return nil
}

// Production is server.env = "production": the cloud's nodes.
func (s ServerConfig) Production() bool { return s.Env == "production" }

// validateOrigin is the api's side of Epic 50 story 50.31: in production its listeners are
// on loopback (or a private address, Phase 2's load balancer) and only Cloudflare reaches
// them, so it needs the peers whose CF-Connecting-IP it believes; the aop fallback needs its
// CA.
func validateOrigin(s ServerConfig) error {
	switch s.OriginMode {
	case OriginTunnel, "":
	case OriginAOP:
		if s.AOPCAFile == "" {
			return fmt.Errorf("%w: server.aop_ca_file (server.origin_mode = \"aop\" checks Cloudflare's client certificate against it)", ErrMissing)
		}
		if _, err := os.Stat(s.AOPCAFile); err != nil {
			return fmt.Errorf("config: server.aop_ca_file %q: %w", s.AOPCAFile, err)
		}
	default:
		return fmt.Errorf("config: server.origin_mode %q: it is \"tunnel\" or \"aop\"", s.OriginMode)
	}
	if !s.Production() {
		return nil
	}
	for _, l := range []struct{ key, addr string }{
		{"server.listen_addr", s.ListenAddr},
		{"server.admin_listen_addr", s.AdminListenAddr},
	} {
		if l.addr == "" {
			continue
		}
		if err := ValidateOriginListen(l.addr); err != nil {
			return fmt.Errorf("config: %s %q in production: %w", l.key, l.addr, err)
		}
	}
	if len(s.TrustedProxies) == 0 {
		return fmt.Errorf("%w: server.trusted_proxies (in production the api is reached only through Cloudflare: list the local cloudflared, [\"127.0.0.1/32\", \"::1/128\"])", ErrMissing)
	}
	return nil
}

// ValidateOriginListen accepts host:port with a loopback or private IP host: never a
// wildcard, a name or a public address.
func ValidateOriginListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.IsUnspecified() || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return errors.New("it must be a loopback or private address: only the Tunnel reaches the api")
	}
	return nil
}

// Warnings are settings that do nothing where they are (logged at start).
func Warnings(c Config) []string {
	var w []string
	if c.Server.OriginMode != OriginAOP && c.Server.AOPCAFile != "" {
		w = append(w, "server.aop_ca_file is set but server.origin_mode is not \"aop\": it is not used")
	}
	return w
}
