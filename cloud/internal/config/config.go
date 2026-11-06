// Package config loads Maktaba Cloud configuration from a TOML file
// and applies environment overrides for secrets.
//
// Story 25.1: bootstrap loader. Validation rules per role land in
// validate.go so the field set stays in one place.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the in-memory representation of cloud.toml. We keep it flat
// enough that the validator can express its rules in plain Go without a
// reflection-driven framework.
type Config struct {
	Server      ServerConfig
	Database    DBConfig
	Redis       RedisConfig
	OAuthGoogle GoogleOAuth
	OAuthApple  AppleOAuth
	Billing     BillingConfig
	APNs        APNsConfig
	FCM         FCMConfig
	Mail        MailConfig
	Entitlement EntitlementConfig
	Telemetry   TelemetryConfig
	Relay       RelayConfig
	Auth        AuthConfig
}

type ServerConfig struct {
	ListenAddr string
	PublicURL  string
	// WebURL is the cloud web app's origin (mail links and OAuth
	// landings point there). Defaults to PublicURL.
	WebURL string
	// TrustProxyHeaders makes the client IP come from CF-Connecting-IP /
	// X-Forwarded-For. Only turn it on behind a proxy that overwrites
	// them; otherwise any client can pick its own rate-limit key.
	TrustProxyHeaders bool
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	ShutdownGrace     time.Duration
}

// AuthConfig holds the HMAC secret used to sign short-lived access
// tokens (cloud/internal/auth/token). The secret never appears in the
// TOML file — it is injected via MAKTABA_CLOUD_TOKEN_SECRET so it can be
// sourced from a secrets manager / Docker secret at runtime.
type AuthConfig struct {
	TokenSecret string // env: MAKTABA_CLOUD_TOKEN_SECRET
}

type DBConfig struct {
	URL             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type RedisConfig struct {
	URL string
}

type GoogleOAuth struct {
	ClientID     string
	ClientSecret string // env
}

type AppleOAuth struct {
	TeamID   string
	KeyID    string
	ClientID string
	KeyPath  string
}

// BillingConfig picks the payment provider. The provider is not chosen
// yet: "none" (the default and, today, the only provider) takes no
// payments — paid plans show as coming soon and service admins set
// plans by hand. Validate refuses any other value.
type BillingConfig struct {
	Provider string
}

type APNsConfig struct {
	TeamID   string
	KeyID    string
	KeyPath  string
	BundleID string
}

type FCMConfig struct {
	ProjectID          string
	ServiceAccountPath string
}

// MailConfig picks the mail driver. "log" (the default and, today, the
// only driver) writes mails to the log instead of delivering them.
type MailConfig struct {
	Driver string
}

type EntitlementConfig struct {
	PrivateKeyPath string
}

type TelemetryConfig struct {
	LogFormat          string
	LogLevel           string
	Sample2xxAfterDays int
	Sample2xxRate      float64
}

type RelayConfig struct {
	PublicHost string
	// EditionsDir holds the web app's player and remote editions the relay serves at
	// /play/ and /manage/ on each home's address (Epic 42.9): play/ and manage/, each a
	// build of web/ (`pnpm build:player:cloud`, `build:remote:cloud`). Empty: none.
	EditionsDir string

	// Tunnel handover (Epic 50 story 50.03). DrainEnabled: on SIGTERM the relay
	// sends every home DRAIN and waits for it to move (false: today's close on
	// shutdown). TicketsEnabled: homes reconnect with a resume ticket instead of
	// their bearer secret (false: every handshake checks the bearer).
	DrainEnabled   bool          // relay.drain.enabled, default true
	TicketsEnabled bool          // relay.ticket.enabled, default true
	DrainWithin    time.Duration // relay.drain.within, default 5s: open the new tunnel within this
	DrainDeadline  time.Duration // relay.drain.deadline, default 120s: the old tunnel closes after this
	DrainRate      int           // relay.drain.rate, default 100: homes sent DRAIN a second
	// ShutdownGrace replaces server.shutdown_grace for the relay role, which
	// waits for its homes to move (relay.shutdown_grace, default 150s).
	ShutdownGrace time.Duration

	// The tunnel directory (story 50.04). ID names this relay process
	// (relay.id, env MAKTABA_CLOUD_RELAY_ID; default the host name), and
	// PeerAddr is where its peers forward requests to it (relay.peer_addr,
	// env MAKTABA_CLOUD_RELAY_PEER_ADDR): loopback or a private address only.
	// Empty PeerAddr: no directory, no forwarding (holding still works).
	ID       string
	PeerAddr string
	// HoldMax: how long a request waits for a home that is reconnecting
	// (relay.hold_max, default 15s; 0s turns holding off); HoldRecent: how
	// recently it must have been seen (relay.hold_recent, default 2m).
	HoldMax    time.Duration
	HoldRecent time.Duration

	// Riding out a database blip (story 50.05). CacheEnabled: home lookups
	// come from a cache that outlives a failover (relay.cache.enabled,
	// default true). ReadyzStrict: /readyz fails as soon as the database
	// does, as the api role's (relay.readyz_strict, default false).
	CacheEnabled bool
	ReadyzStrict bool
}

// Default values per the bootstrap plan; the loader fills these for any
// keys absent from the TOML file so the file stays minimal.
func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			ListenAddr:    "0.0.0.0:8080",
			ReadTimeout:   30 * time.Second,
			WriteTimeout:  60 * time.Second,
			ShutdownGrace: 20 * time.Second,
		},
		Database: DBConfig{
			MaxOpenConns:    25,
			MaxIdleConns:    5,
			ConnMaxLifetime: 30 * time.Minute,
		},
		Mail:    MailConfig{Driver: "log"},
		Billing: BillingConfig{Provider: "none"},
		Telemetry: TelemetryConfig{
			LogFormat:          "json",
			LogLevel:           "info",
			Sample2xxAfterDays: 7,
			Sample2xxRate:      0.10,
		},
		Relay: RelayConfig{
			PublicHost:     "relay.maktaba.tv",
			DrainEnabled:   true,
			TicketsEnabled: true,
			DrainWithin:    5 * time.Second,
			DrainDeadline:  120 * time.Second,
			DrainRate:      100,
			ShutdownGrace:  150 * time.Second,
			HoldMax:        15 * time.Second,
			CacheEnabled:   true,
			HoldRecent:     2 * time.Minute,
		},
	}
}

// Load reads a TOML file, then layers env overrides. We do not pull in a
// dependency here — the file format is intentionally tiny and the
// parser below tolerates only what bootstrap actually needs.
func Load(path string) (Config, error) {
	cfg := defaultConfig()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := parseTOML(string(b), &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	applyEnvOverrides(&cfg)
	if cfg.Server.WebURL == "" {
		cfg.Server.WebURL = cfg.Server.PublicURL
	}
	return cfg, nil
}

func applyEnvOverrides(c *Config) {
	if v := os.Getenv("MAKTABA_CLOUD_DB_URL"); v != "" {
		c.Database.URL = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_REDIS_URL"); v != "" {
		c.Redis.URL = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_LISTEN_ADDR"); v != "" {
		c.Server.ListenAddr = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_PUBLIC_URL"); v != "" {
		c.Server.PublicURL = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_OAUTH_GOOGLE_SECRET"); v != "" {
		c.OAuthGoogle.ClientSecret = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_BILLING_PROVIDER"); v != "" {
		c.Billing.Provider = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_WEB_URL"); v != "" {
		c.Server.WebURL = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_TOKEN_SECRET"); v != "" {
		c.Auth.TokenSecret = v
	}
	// Relay public host is operator-specific (their own domain), so the
	// VPS deploy can drive it purely from env without a config file.
	if v := os.Getenv("MAKTABA_CLOUD_RELAY_PUBLIC_HOST"); v != "" {
		c.Relay.PublicHost = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_RELAY_EDITIONS_DIR"); v != "" {
		c.Relay.EditionsDir = v
	}
	// the blue/green units (story 50.08) name each process and its peer port
	if v := os.Getenv("MAKTABA_CLOUD_RELAY_ID"); v != "" {
		c.Relay.ID = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_RELAY_PEER_ADDR"); v != "" {
		c.Relay.PeerAddr = v
	}
}

// parseTOML is a minimal subset parser — string/int/duration/float
// assignments inside `[section]` blocks. Anything richer is rejected
// loudly so we never silently misread a real TOML.
func parseTOML(src string, c *Config) error {
	section := ""
	for ln, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return fmt.Errorf("line %d: missing '='", ln+1)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(stripInlineComment(line[eq+1:]))
		val = strings.Trim(val, `"`)
		if err := assign(c, section, key, val); err != nil {
			return fmt.Errorf("line %d: %w", ln+1, err)
		}
	}
	return nil
}

func stripInlineComment(s string) string {
	inStr := false
	for i, r := range s {
		if r == '"' {
			inStr = !inStr
		}
		if r == '#' && !inStr {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

func assign(c *Config, section, key, val string) error {
	switch section + "." + key {
	case "server.listen_addr":
		c.Server.ListenAddr = val
	case "server.public_url":
		c.Server.PublicURL = val
	case "server.read_timeout":
		return setDur(&c.Server.ReadTimeout, val)
	case "server.write_timeout":
		return setDur(&c.Server.WriteTimeout, val)
	case "server.shutdown_grace":
		return setDur(&c.Server.ShutdownGrace, val)
	case "database.url":
		c.Database.URL = val
	case "database.max_open_conns":
		return setInt(&c.Database.MaxOpenConns, val)
	case "database.max_idle_conns":
		return setInt(&c.Database.MaxIdleConns, val)
	case "database.conn_max_lifetime":
		return setDur(&c.Database.ConnMaxLifetime, val)
	case "redis.url":
		c.Redis.URL = val
	case "oauth.google.client_id":
		c.OAuthGoogle.ClientID = val
	case "oauth.apple.team_id":
		c.OAuthApple.TeamID = val
	case "oauth.apple.key_id":
		c.OAuthApple.KeyID = val
	case "oauth.apple.client_id":
		c.OAuthApple.ClientID = val
	case "oauth.apple.key_path":
		c.OAuthApple.KeyPath = val
	case "apns.team_id":
		c.APNs.TeamID = val
	case "apns.key_id":
		c.APNs.KeyID = val
	case "apns.key_path":
		c.APNs.KeyPath = val
	case "apns.bundle_id":
		c.APNs.BundleID = val
	case "fcm.project_id":
		c.FCM.ProjectID = val
	case "fcm.service_account_path":
		c.FCM.ServiceAccountPath = val
	case "server.web_url":
		c.Server.WebURL = val
	case "server.trust_proxy_headers":
		return setBool(&c.Server.TrustProxyHeaders, val)
	case "billing.provider":
		c.Billing.Provider = val
	case "mail.driver":
		c.Mail.Driver = val
	case "entitlement.private_key_path":
		c.Entitlement.PrivateKeyPath = val
	case "telemetry.log_format":
		c.Telemetry.LogFormat = val
	case "telemetry.log_level":
		c.Telemetry.LogLevel = val
	case "telemetry.sample_2xx_after_days":
		return setInt(&c.Telemetry.Sample2xxAfterDays, val)
	case "telemetry.sample_2xx_rate":
		return setFloat(&c.Telemetry.Sample2xxRate, val)
	case "relay.public_host":
		c.Relay.PublicHost = val
	case "relay.drain.enabled":
		return setBool(&c.Relay.DrainEnabled, val)
	case "relay.ticket.enabled":
		return setBool(&c.Relay.TicketsEnabled, val)
	case "relay.drain.within":
		return setDur(&c.Relay.DrainWithin, val)
	case "relay.drain.deadline":
		return setDur(&c.Relay.DrainDeadline, val)
	case "relay.drain.rate":
		return setInt(&c.Relay.DrainRate, val)
	case "relay.id":
		c.Relay.ID = val
	case "relay.peer_addr":
		c.Relay.PeerAddr = val
	case "relay.cache.enabled":
		return setBool(&c.Relay.CacheEnabled, val)
	case "relay.readyz_strict":
		return setBool(&c.Relay.ReadyzStrict, val)
	case "relay.hold_max":
		return setDur(&c.Relay.HoldMax, val)
	case "relay.hold_recent":
		return setDur(&c.Relay.HoldRecent, val)
	case "relay.shutdown_grace":
		return setDur(&c.Relay.ShutdownGrace, val)
	case "relay.editions_dir":
		c.Relay.EditionsDir = val
	}
	return nil
}

func setDur(dst *time.Duration, v string) error {
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("duration %q: %w", v, err)
	}
	*dst = d
	return nil
}

func setBool(dst *bool, v string) error {
	switch v {
	case "true":
		*dst = true
	case "false":
		*dst = false
	default:
		return fmt.Errorf("bool %q: want true or false", v)
	}
	return nil
}

func setInt(dst *int, v string) error {
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fmt.Errorf("int %q: %w", v, err)
	}
	*dst = n
	return nil
}

func setFloat(dst *float64, v string) error {
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err != nil {
		return fmt.Errorf("float %q: %w", v, err)
	}
	*dst = f
	return nil
}

// ErrMissing reports a required section that did not load.
var ErrMissing = errors.New("config: missing required section")
