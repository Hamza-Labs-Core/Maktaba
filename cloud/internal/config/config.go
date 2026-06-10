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
	Stripe      StripeConfig
	APNs        APNsConfig
	FCM         FCMConfig
	Admin       AdminConfig
	Entitlement EntitlementConfig
	Telemetry   TelemetryConfig
	Relay       RelayConfig
}

type ServerConfig struct {
	ListenAddr    string
	PublicURL     string
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	ShutdownGrace time.Duration
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

type StripeConfig struct {
	SecretKey      string // env
	WebhookSecret  string // env
	PublishableKey string
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

type AdminConfig struct {
	AllowedEmailDomain string
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
		Admin: AdminConfig{AllowedEmailDomain: "hamzalabs.com"},
		Telemetry: TelemetryConfig{
			LogFormat:          "json",
			LogLevel:           "info",
			Sample2xxAfterDays: 7,
			Sample2xxRate:      0.10,
		},
		Relay: RelayConfig{PublicHost: "relay.maktaba.app"},
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
	if v := os.Getenv("MAKTABA_CLOUD_STRIPE_SECRET"); v != "" {
		c.Stripe.SecretKey = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_STRIPE_WEBHOOK_SECRET"); v != "" {
		c.Stripe.WebhookSecret = v
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
	case "stripe.publishable_key":
		c.Stripe.PublishableKey = val
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
	case "admin.allowed_email_domain":
		c.Admin.AllowedEmailDomain = val
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
