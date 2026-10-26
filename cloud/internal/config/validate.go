package config

import (
	"fmt"
	"strings"
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
		if err := validateStripe(c.Stripe); err != nil {
			return err
		}
	case RoleRelay:
		if c.Server.ListenAddr == "" {
			return fmt.Errorf("%w: [server].listen_addr", ErrMissing)
		}
		if c.Relay.PublicHost == "" {
			return fmt.Errorf("%w: [relay].public_host", ErrMissing)
		}
	case RoleWorker:
		// Worker uses control port only; no public listener required.
	default:
		return fmt.Errorf("config: unknown role %q", role)
	}
	return nil
}

// ErrLiveStripe refuses a live Stripe key the operator did not opt into.
var ErrLiveStripe = fmt.Errorf("config: a live Stripe key needs [stripe].live_mode = true")

// validateStripe keeps the api role on Stripe test mode unless live mode
// is switched on explicitly, and keeps a test key out of live mode.
func validateStripe(s StripeConfig) error {
	live := strings.HasPrefix(s.SecretKey, "sk_live_") || strings.HasPrefix(s.SecretKey, "rk_live_")
	switch {
	case live && !s.LiveMode:
		return ErrLiveStripe
	case s.LiveMode && s.SecretKey != "" && !live:
		return fmt.Errorf("config: [stripe].live_mode is on but the secret key is not a live key")
	}
	return nil
}
