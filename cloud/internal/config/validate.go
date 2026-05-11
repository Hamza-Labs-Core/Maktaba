package config

import "fmt"

// Role identifies which set of features this process is providing.
type Role string

const (
	RoleAPI    Role = "api"
	RoleRelay  Role = "relay"
	RoleWorker Role = "worker"
)

// Validate applies role-specific required-field checks. The api role is
// the strictest: it owns identity, billing, and admin surfaces, so it
// needs oauth + stripe + admin domain configured.
func Validate(c Config, role Role) error {
	if c.Database.URL == "" {
		return fmt.Errorf("%w: [database].url", ErrMissing)
	}
	switch role {
	case RoleAPI:
		if c.Server.ListenAddr == "" {
			return fmt.Errorf("%w: [server].listen_addr", ErrMissing)
		}
		if c.OAuthGoogle.ClientID == "" && c.OAuthApple.ClientID == "" {
			return fmt.Errorf("%w: at least one of [oauth.google] or [oauth.apple]", ErrMissing)
		}
		if c.Admin.AllowedEmailDomain == "" {
			return fmt.Errorf("%w: [admin].allowed_email_domain", ErrMissing)
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
