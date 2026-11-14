package config

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if c.Database.MaxOpenConns != 25 {
		t.Errorf("default MaxOpenConns = %d, want 25", c.Database.MaxOpenConns)
	}
	if c.Telemetry.Sample2xxRate != 0.10 {
		t.Errorf("default sample rate = %v, want 0.10", c.Telemetry.Sample2xxRate)
	}
}

func TestLoad_TOML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cloud.toml")
	if err := os.WriteFile(p, []byte(`
[server]
listen_addr = "127.0.0.1:1234"
read_timeout = "5s"

[database]
url = "postgres://x"
max_open_conns = 7
`), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Server.ListenAddr != "127.0.0.1:1234" {
		t.Errorf("listen_addr = %q", c.Server.ListenAddr)
	}
	if c.Database.MaxOpenConns != 7 {
		t.Errorf("max_open_conns = %d", c.Database.MaxOpenConns)
	}
	if c.Server.ReadTimeout.Seconds() != 5 {
		t.Errorf("read_timeout = %v", c.Server.ReadTimeout)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("MAKTABA_CLOUD_DB_URL", "postgres://from-env")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Database.URL != "postgres://from-env" {
		t.Errorf("env override not applied: %q", c.Database.URL)
	}
}

func TestLoad_TokenSecretEnvOverride(t *testing.T) {
	// MAKTABA_CLOUD_TOKEN_SECRET is env-only (no TOML key) — config is
	// the single source of truth the api role reads from.
	t.Setenv("MAKTABA_CLOUD_TOKEN_SECRET", "this-is-at-least-32-bytes-long!!")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth.TokenSecret != "this-is-at-least-32-bytes-long!!" {
		t.Errorf("token secret env override not applied: %q", c.Auth.TokenSecret)
	}
}

func TestValidate_RoleAPI(t *testing.T) {
	c := Config{}
	if err := Validate(c, RoleAPI); err == nil {
		t.Error("Validate empty/api should fail")
	}
	c.Database.URL = "postgres://x"
	c.Server.ListenAddr = ":8080"
	c.Mail.Driver = "log"
	if err := Validate(c, RoleAPI); err != nil {
		t.Errorf("Validate(api) ok config: %v", err)
	}
}

// The payment provider is not chosen yet: [billing].provider defaults
// to "none", and the api role refuses any provider it does not ship.
func TestValidate_BillingProvider(t *testing.T) {
	c := Config{}
	c.Database.URL = "postgres://x"
	c.Server.ListenAddr = ":8080"
	c.Mail.Driver = "log"
	for _, tc := range []struct {
		provider string
		ok       bool
	}{
		{"", true},
		{"none", true},
		{"None", false},
		{"manual", false},
		{"some-provider", false},
	} {
		c.Billing.Provider = tc.provider
		err := Validate(c, RoleAPI)
		if (err == nil) != tc.ok {
			t.Errorf("provider=%q: err=%v, want ok=%v", tc.provider, err, tc.ok)
		}
		if !tc.ok && !errors.Is(err, ErrUnknownBillingProvider) {
			t.Errorf("provider=%q: err=%v, want ErrUnknownBillingProvider", tc.provider, err)
		}
	}
}

func TestLoad_BillingProvider(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Billing.Provider != "none" {
		t.Errorf("default provider = %q, want none", c.Billing.Provider)
	}
	p := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(p, []byte("[billing]\nprovider = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(p); err != nil || c.Billing.Provider != "none" {
		t.Errorf("toml provider: %q %v", c.Billing.Provider, err)
	}
	t.Setenv("MAKTABA_CLOUD_BILLING_PROVIDER", "elsewhere")
	if c, err = Load(p); err != nil || c.Billing.Provider != "elsewhere" {
		t.Errorf("env provider: %q %v", c.Billing.Provider, err)
	}
	c.Database.URL = "postgres://x"
	if err := Validate(c, RoleAPI); !errors.Is(err, ErrUnknownBillingProvider) {
		t.Errorf("an unknown provider from env: %v", err)
	}
}

func TestValidate_RoleWorkerLooser(t *testing.T) {
	c := Config{}
	c.Database.URL = "postgres://x"
	if err := Validate(c, RoleWorker); err != nil {
		t.Errorf("Validate(worker) only needs DB: %v", err)
	}
}

// Story 50.03: the relay's handover switches and timings, with their defaults.
func TestLoad_RelayDrain(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	r := c.Relay
	if !r.DrainEnabled || !r.TicketsEnabled || r.DrainWithin.Seconds() != 5 || r.DrainDeadline.Seconds() != 120 || r.DrainRate != 100 || r.ShutdownGrace.Seconds() != 150 {
		t.Errorf("defaults: %+v", r)
	}
	p := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(p, []byte(`
[relay]
shutdown_grace = "30s"

[relay.drain]
enabled = false
within = "2s"
deadline = "10s"
rate = 50

[relay.ticket]
enabled = false
`), 0644); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r = c.Relay
	if r.DrainEnabled || r.TicketsEnabled || r.DrainWithin.Seconds() != 2 || r.DrainDeadline.Seconds() != 10 || r.DrainRate != 50 || r.ShutdownGrace.Seconds() != 30 {
		t.Errorf("from the file: %+v", r)
	}
}

// Story 50.04: the directory's keys, the env names the blue/green units set,
// and a peer address that is never public.
func TestRelayDirectoryConfig(t *testing.T) {
	t.Setenv("MAKTABA_CLOUD_RELAY_ID", "a-green")
	t.Setenv("MAKTABA_CLOUD_RELAY_PEER_ADDR", "127.0.0.1:8445")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Relay.ID != "a-green" || c.Relay.PeerAddr != "127.0.0.1:8445" || c.Relay.HoldMax.Seconds() != 15 || c.Relay.HoldRecent.Minutes() != 2 {
		t.Errorf("relay: %+v", c.Relay)
	}
	for addr, ok := range map[string]bool{"": true, "127.0.0.1:8444": true, "10.0.1.3:8443": true, "[::1]:9": true,
		"0.0.0.0:8444": false, ":8444": false, "203.0.113.5:8444": false, "relay.example:1": false, "nonsense": false} {
		if err := ValidatePeerAddr(addr); (err == nil) != ok {
			t.Errorf("ValidatePeerAddr(%q) = %v", addr, err)
		}
	}
}

// Story 50.05: the cache and the lenient readiness, on by default.
func TestRelayBlipConfig(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Relay.CacheEnabled || c.Relay.ReadyzStrict {
		t.Errorf("defaults: cache %v strict %v", c.Relay.CacheEnabled, c.Relay.ReadyzStrict)
	}
	p := filepath.Join(t.TempDir(), "c.toml")
	_ = os.WriteFile(p, []byte("[relay]\nreadyz_strict = true\n\n[relay.cache]\nenabled = false\n"), 0644)
	if c, err = Load(p); err != nil || c.Relay.CacheEnabled || !c.Relay.ReadyzStrict {
		t.Errorf("from the file: %+v %v", c.Relay, err)
	}
}

// Epic 50 story 50.19: the plans are not enforced until relay.plans.enforce; the stream limit
// is on once they are.
func TestLoad_RelayPlans(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Relay.PlansEnforce || !c.Relay.StreamLimitEnabled {
		t.Errorf("defaults: enforce %v, stream limit %v", c.Relay.PlansEnforce, c.Relay.StreamLimitEnabled)
	}
	p := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(p, []byte("[relay.plans]\nenforce = true\n\n[relay.stream_limit]\nenabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Relay.PlansEnforce || c.Relay.StreamLimitEnabled {
		t.Errorf("from the file: enforce %v, stream limit %v", c.Relay.PlansEnforce, c.Relay.StreamLimitEnabled)
	}
}

// Story 50.30 T06: the auth pages' bot check is never on without its secret; its keys parse,
// the secret comes from the environment only, and the web origin defaults to the web app's.
func TestTurnstileConfig(t *testing.T) {
	c := defaultConfig()
	if err := parseTOML("[turnstile]\nenabled = true\nsite_key = \"0x4AAA\"\nweb_origins = [\"https://app.maktaba.tv\", \"https://admin.maktaba.tv\"]\n", &c); err != nil {
		t.Fatal(err)
	}
	if !c.Turnstile.Enabled || c.Turnstile.SiteKey != "0x4AAA" || len(c.Turnstile.WebOrigins) != 2 {
		t.Fatalf("%+v", c.Turnstile)
	}
	if err := parseTOML("[turnstile]\nsecret = \"in the file\"\n", &c); err == nil {
		t.Fatal("a secret in the file was accepted")
	}
	c.Database.URL = "postgres://x"
	c.Server.ListenAddr = "127.0.0.1:8090"
	err := Validate(c, RoleAPI)
	if err == nil || !strings.Contains(err.Error(), "MAKTABA_CLOUD_TURNSTILE_SECRET") || !strings.Contains(err.Error(), "turnstile.enabled") {
		t.Fatalf("on without a secret: %v", err)
	}
	t.Setenv("MAKTABA_CLOUD_TURNSTILE_SECRET", "from-env")
	applyEnvOverrides(&c)
	if err := Validate(c, RoleAPI); err != nil {
		t.Fatalf("with the secret: %v", err)
	}
	path := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(path, []byte("[database]\nurl = \"postgres://x\"\n[server]\nweb_url = \"https://app.maktaba.tv/\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil || len(l.Turnstile.WebOrigins) != 1 || l.Turnstile.WebOrigins[0] != "https://app.maktaba.tv" || l.Turnstile.Enabled {
		t.Fatalf("defaults: %+v %v", l.Turnstile, err)
	}
}

// Epic 50 story 50.31, kept when Epic 59 story 59.08 moved the parser to shared/go/clientip:
// the cloud's server.trusted_proxies takes CIDRs only (a bare address is refused, so a list
// says what it means), masked.
func TestParsePrefixesCIDRsOnly(t *testing.T) {
	got, err := ParsePrefixes([]string{" 10.1.2.3/8 ", "::1/128"})
	if err != nil || len(got) != 2 || got[0].String() != "10.0.0.0/8" || got[1].String() != "::1/128" {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"127.0.0.1", "", "not-a-cidr"} {
		if _, err := ParsePrefixes([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Story 50.20: the broadcast keys, their defaults, and the broadcasting gate (T07).
func TestBroadcastConfig(t *testing.T) {
	c := defaultConfig()
	if c.Broadcast.Enabled || !c.Broadcast.PlansEnforce || !c.Broadcast.PAYGReports {
		t.Fatalf("defaults: %+v", c.Broadcast)
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := parseTOML("[broadcast]\nenabled = true\nedge_public_key = \""+key+"\"\n[broadcast.plans]\nenforce = false\n[billing.broadcast_payg]\nenabled = false\n", &c); err != nil {
		t.Fatal(err)
	}
	if !c.Broadcast.Enabled || c.Broadcast.PlansEnforce || c.Broadcast.PAYGReports || c.Broadcast.EdgePublicKey != key {
		t.Fatalf("parsed: %+v", c.Broadcast)
	}
	// every broadcast price is TBD: switching broadcasting on is refused, naming the fields
	err := ValidateBroadcast(c.Broadcast)
	if err == nil || !strings.Contains(err.Error(), "broadcaster.price") || !strings.Contains(err.Error(), "broadcaster.payg_price_per_100h") {
		t.Fatalf("the gate with TBD prices: %v", err)
	}
	if err := ValidateBroadcast(BroadcastConfig{EdgePublicKey: "c2hvcnQ="}); err == nil {
		t.Fatal("a short edge key was accepted")
	}
	if err := ValidateBroadcast(BroadcastConfig{EdgePublicKey: key}); err != nil {
		t.Fatalf("a good key, gate shut: %v", err)
	}
}
