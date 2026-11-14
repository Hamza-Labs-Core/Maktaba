// Package config loads Maktaba Cloud configuration from a TOML file
// and applies environment overrides for secrets.
//
// Story 25.1: bootstrap loader. Validation rules per role land in
// validate.go so the field set stays in one place.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
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
	Direct      DirectConfig
	Turnstile   TurnstileConfig
	Admin       AdminConfig
	Broadcast   BroadcastConfig
}

// BroadcastConfig is the cloud's side of the broadcast edge (Epic 50 story 50.21): the key that
// signs ingest tokens and control posts (its public half goes in the edge's CLOUD_PUBKEYS), the
// ingest host homes push to, and the edge's public key its posts to the API are checked with.
type BroadcastConfig struct {
	Enabled bool // broadcast.enabled: issue ingest tokens (default false)
	// KeyPath is broadcast.key_path: the Ed25519 seed file (base64url or 32 raw bytes, 0600).
	KeyPath string
	// IngestURL is broadcast.ingest_url, default https://ingest.maktaba.tv.
	IngestURL string
	// EdgePublicKey is broadcast.edge_public_key: base64url Ed25519 (EDGE_SIGNING_KEY's half).
	EdgePublicKey string
}

// AdminConfig is the operator console's own settings (Epic 50 story 50.33 §7).
type AdminConfig struct {
	// IPAllowlist is admin.ip_allowlist: when set, the admin listener answers only client IPs
	// inside one of these prefixes (403 ip_not_allowed otherwise). Empty, the default, is off.
	IPAllowlist []netip.Prefix
}

// TurnstileConfig is the bot check on the auth pages (Epic 50 story 50.30 §4): off by default
// (dev and CI); production turns it on with the widget's site key, and the secret comes from
// the environment only (MAKTABA_CLOUD_TURNSTILE_SECRET). On without a secret, the api role
// refuses to start.
type TurnstileConfig struct {
	Enabled bool   // turnstile.enabled
	SiteKey string // turnstile.site_key: public, served to the web pages
	Secret  string // env MAKTABA_CLOUD_TURNSTILE_SECRET only
	// WebOrigins are the origins asked for a token (turnstile.web_origins; default the web
	// app's, server.web_url). Native apps send no Origin and are never asked.
	WebOrigins []string
}

// DirectConfig is direct remote access (Epic 50 story 50.15): the direct-name domains (never
// maktaba.tv; adding one is configuration only), their name servers, and the ACME CAs.
type DirectConfig struct {
	Enabled    bool          // direct.enabled: list direct connections, run the checks (default false)
	Domains    []string      // direct.domains = ["direct-a.example", ...]
	DNSListen  string        // direct.dns_listen, default ":53" (the dns role)
	NS1, NS2   string        // direct.ns1, direct.ns2: nodes A's and B's primary IPs
	Budget     int           // direct.weekly_budget, default 45 new certificates a domain a week
	CheckEvery time.Duration // direct.check_every, default 15m
	// ACME is each CA's settings by name (letsencrypt, zerossl, google): [direct.acme.<ca>].
	ACME map[string]DirectACME
}

// DirectACME is one CA: its directory, the account's key file (made on first use) and, for
// ZeroSSL and Google, the external account binding (its HMAC from the environment:
// MAKTABA_CLOUD_DIRECT_<CA>_EAB_HMAC, never the file).
type DirectACME struct {
	Directory  string
	AccountKey string
	EABKeyID   string
	EABHMAC    string
	Contact    string
}

type ServerConfig struct {
	ListenAddr string
	PublicURL  string
	// WebURL is the cloud web app's origin (mail links and OAuth
	// landings point there). Defaults to PublicURL.
	WebURL string
	// Env is "production" on the cloud's nodes (server.env); anything else is a development
	// or CI run. Production refuses a public listener and an empty TrustedProxies (50.31).
	Env string
	// AdminListenAddr is the operator console's own listener (server.admin_listen_addr,
	// Epic 50 story 50.32): /v1/admin/* and /v1/staff/* only. Empty means none, and the
	// console has no address at all.
	AdminListenAddr string
	// AdminHost is the one Host the admin listener answers (server.admin_host,
	// admin.maktaba.tv); any other is 404. Required with AdminListenAddr.
	AdminHost string
	// AdminOrigin is the Origin a state-changing admin request must carry
	// (server.admin_origin); empty means "https://" + AdminHost. Dev sets the Vite server's.
	AdminOrigin string
	// UserHosts are the Hosts the user listener answers (server.user_hosts: api.maktaba.tv,
	// app.maktaba.tv, maktaba.tv); any other is 404. Empty means any (development).
	UserHosts []string
	// TrustedProxies are the peers whose CF-Connecting-IP is believed (server.trusted_proxies,
	// CIDRs): the local cloudflared, or Caddy after its origin checks. Empty means the client
	// IP is always the TCP peer. It replaces server.trust_proxy_headers (Epic 50 story 50.31).
	TrustedProxies []netip.Prefix
	// OriginMode is how Cloudflare reaches the api (server.origin_mode): "tunnel" (the
	// default) or "aop", the fallback (authenticated origin pulls plus Cloudflare's ranges, at
	// Caddy). AOPCAFile is the CA Caddy checks Cloudflare's client certificate against.
	OriginMode    string
	AOPCAFile     string
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	ShutdownGrace time.Duration
}

// AuthConfig holds the HMAC secret used to sign short-lived access
// tokens (cloud/internal/auth/token). The secret never appears in the
// TOML file — it is injected via MAKTABA_CLOUD_TOKEN_SECRET so it can be
// sourced from a secrets manager / Docker secret at runtime.
type AuthConfig struct {
	TokenSecret string // env: MAKTABA_CLOUD_TOKEN_SECRET
	// StaffTokenSecret signs the console's staff access tokens (story 50.33 §2), a different
	// key from TokenSecret's; StaffSecretKey (32 bytes, hex or base64) seals each staff TOTP
	// secret at rest. Both come from the environment only.
	StaffTokenSecret string // env: MAKTABA_CLOUD_STAFF_TOKEN_SECRET
	StaffSecretKey   string // env: MAKTABA_CLOUD_STAFF_SECRET_KEY
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

	// The plans (Epic 50 story 50.19 §3). PlansEnforce (relay.plans.enforce, default false):
	// paid homes are capped, paced and stream-limited as their plan says; off, they are
	// forwarded in full and their bytes still counted. A plan without the relay (Free) is
	// refused either way. StreamLimitEnabled (relay.stream_limit.enabled, default true): a new
	// stream past the plan's limit is refused (counting goes on when off).
	PlansEnforce       bool
	StreamLimitEnabled bool

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
		Direct:    DirectConfig{DNSListen: ":53", Budget: 45, CheckEvery: 15 * time.Minute},
		Broadcast: BroadcastConfig{IngestURL: "https://ingest.maktaba.tv"},
		Server: ServerConfig{
			ListenAddr:    "0.0.0.0:8080",
			OriginMode:    OriginTunnel,
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

			StreamLimitEnabled: true,
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
	if len(cfg.Turnstile.WebOrigins) == 0 && cfg.Server.WebURL != "" {
		cfg.Turnstile.WebOrigins = []string{strings.TrimSuffix(cfg.Server.WebURL, "/")}
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
	if v := os.Getenv("MAKTABA_CLOUD_STAFF_TOKEN_SECRET"); v != "" {
		c.Auth.StaffTokenSecret = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_STAFF_SECRET_KEY"); v != "" {
		c.Auth.StaffSecretKey = v
	}
	if v := os.Getenv("MAKTABA_CLOUD_TURNSTILE_SECRET"); v != "" {
		c.Turnstile.Secret = v
	}
	// the direct CAs' EAB secrets (story 50.15) are never in the file
	for name, a := range c.Direct.ACME {
		if v := os.Getenv("MAKTABA_CLOUD_DIRECT_" + strings.ToUpper(name) + "_EAB_HMAC"); v != "" {
			a.EABHMAC = v
			c.Direct.ACME[name] = a
		}
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

// parseList reads a TOML string array as the flat parser leaves it (its quotes trimmed at the
// ends): ["a", "b"] or a", "b.
func parseList(v string) []string {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]"))
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"`))
		if p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

func assign(c *Config, section, key, val string) error {
	if ca, ok := strings.CutPrefix(section, "direct.acme."); ok {
		if c.Direct.ACME == nil {
			c.Direct.ACME = map[string]DirectACME{}
		}
		a := c.Direct.ACME[ca]
		switch key {
		case "directory":
			a.Directory = val
		case "account_key":
			a.AccountKey = val
		case "eab_kid":
			a.EABKeyID = val
		case "contact":
			a.Contact = val
		default:
			return fmt.Errorf("unknown key %s.%s", section, key)
		}
		c.Direct.ACME[ca] = a
		return nil
	}
	switch section + "." + key {
	case "broadcast.enabled":
		return setBool(&c.Broadcast.Enabled, val)
	case "broadcast.key_path":
		c.Broadcast.KeyPath = val
	case "broadcast.ingest_url":
		c.Broadcast.IngestURL = val
	case "broadcast.edge_public_key":
		c.Broadcast.EdgePublicKey = val
	case "direct.enabled":
		return setBool(&c.Direct.Enabled, val)
	case "direct.domains":
		c.Direct.Domains = parseList(val)
	case "direct.dns_listen":
		c.Direct.DNSListen = val
	case "direct.ns1":
		c.Direct.NS1 = val
	case "direct.ns2":
		c.Direct.NS2 = val
	case "direct.weekly_budget":
		return setInt(&c.Direct.Budget, val)
	case "direct.check_every":
		return setDur(&c.Direct.CheckEvery, val)
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
		return ErrTrustProxyHeaders
	case "server.trusted_proxies":
		ps, err := ParsePrefixes(parseList(val))
		if err != nil {
			return fmt.Errorf("config: server.trusted_proxies: %w", err)
		}
		c.Server.TrustedProxies = ps
	case "server.env":
		c.Server.Env = val
	case "server.admin_listen_addr":
		c.Server.AdminListenAddr = val
	case "server.admin_host":
		c.Server.AdminHost = strings.ToLower(val)
	case "server.admin_origin":
		c.Server.AdminOrigin = strings.TrimSuffix(val, "/")
	case "admin.ip_allowlist":
		ps, err := ParsePrefixes(parseList(val))
		if err != nil {
			return fmt.Errorf("config: admin.ip_allowlist: %w", err)
		}
		c.Admin.IPAllowlist = ps
	case "server.user_hosts":
		for _, h := range parseList(val) {
			c.Server.UserHosts = append(c.Server.UserHosts, strings.ToLower(h))
		}
	case "server.origin_mode":
		c.Server.OriginMode = val
	case "server.aop_ca_file":
		c.Server.AOPCAFile = val
	case "turnstile.enabled":
		return setBool(&c.Turnstile.Enabled, val)
	case "turnstile.site_key":
		c.Turnstile.SiteKey = val
	case "turnstile.web_origins":
		c.Turnstile.WebOrigins = parseList(val)
	case "turnstile.secret":
		// the secret is never in a file: MAKTABA_CLOUD_TURNSTILE_SECRET only
		return fmt.Errorf("turnstile.secret: the secret is not read from the file; set MAKTABA_CLOUD_TURNSTILE_SECRET")
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
	case "relay.plans.enforce":
		return setBool(&c.Relay.PlansEnforce, val)
	case "relay.stream_limit.enabled":
		return setBool(&c.Relay.StreamLimitEnabled, val)
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

// ErrTrustProxyHeaders refuses the old switch: it believed CF-Connecting-IP from any peer.
var ErrTrustProxyHeaders = errors.New("config: server.trust_proxy_headers was replaced by server.trusted_proxies " +
	"(the CIDRs whose CF-Connecting-IP is believed, e.g. [\"127.0.0.1/32\", \"::1/128\"] behind the local cloudflared)")

// The origin modes (server.origin_mode, Epic 50 story 50.31).
const (
	OriginTunnel = "tunnel"
	OriginAOP    = "aop"
)

// AdminOriginOrDefault is the Origin state-changing admin requests must carry.
func (s ServerConfig) AdminOriginOrDefault() string {
	if s.AdminOrigin != "" {
		return s.AdminOrigin
	}
	return "https://" + s.AdminHost
}

// ParsePrefixes reads CIDRs; a bare address is refused, so a list says what it means
// (shared/go/clientip's ParseCIDRs; the home's list also takes bare addresses).
func ParsePrefixes(vs []string) ([]netip.Prefix, error) {
	return clientip.ParseCIDRs(vs)
}
