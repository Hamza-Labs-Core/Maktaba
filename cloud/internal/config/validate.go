package config

import (
	"errors"
	"fmt"
	"net"
)

// Role identifies which set of features this process is providing.
type Role string

const (
	RoleAPI    Role = "api"
	RoleRelay  Role = "relay"
	RoleWorker Role = "worker"
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
