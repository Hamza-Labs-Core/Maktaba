package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mailConfig(t *testing.T, mail string) (Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(p, []byte("[server]\nlisten_addr = \"127.0.0.1:1\"\nweb_url = \"https://app.example.test\"\n[database]\nurl = \"postgres://x\"\n[mail]\n"+mail), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func keyFile(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "resend.key")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const resendTOML = `driver = "resend"
from = "Maktaba <no-reply@maktaba.tv>"
reply_to = "support@maktaba.tv"
allow_recipients = ["@maktaba.tv", "delivered@resend.dev"]
subject_prefix = "[QA]"
daily_limit = 20
monthly_limit = 300
`

func TestMailDefaultsToTheLog(t *testing.T) {
	c, err := mailConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Mail.Driver != "log" || c.Mail.Delivers() || Validate(c, RoleAPI) != nil || Validate(c, RoleWorker) != nil {
		t.Fatalf("%+v: %v", c.Mail, Validate(c, RoleAPI))
	}
}

func TestMailResend(t *testing.T) {
	c, err := mailConfig(t, resendTOML)
	if err != nil {
		t.Fatal(err)
	}
	if m := c.Mail; m.From != "Maktaba <no-reply@maktaba.tv>" || m.ReplyTo != "support@maktaba.tv" || m.SubjectPrefix != "[QA]" ||
		m.DailyLimit != 20 || m.MonthlyLimit != 300 || strings.Join(m.AllowRecipients, " ") != "@maktaba.tv delivered@resend.dev" {
		t.Fatalf("%+v", m)
	}
	// no key: refused
	if err := Validate(c, RoleAPI); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "Resend API key") {
		t.Fatalf("a resend driver without a key: %v", err)
	}
	// the key from the environment
	t.Setenv("MAKTABA_CLOUD_RESEND_API_KEY", "re_x")
	if c, _ = mailConfig(t, resendTOML); Validate(c, RoleAPI) != nil {
		t.Fatal(Validate(c, RoleAPI))
	}
	t.Setenv("MAKTABA_CLOUD_RESEND_API_KEY", "")
	// from a file, trimmed, read again at each use
	p := keyFile(t, "re_one\n")
	t.Setenv("MAKTABA_CLOUD_RESEND_API_KEY_FILE", p)
	if c, _ = mailConfig(t, resendTOML); Validate(c, RoleAPI) != nil {
		t.Fatal(Validate(c, RoleAPI))
	}
	if k, err := c.Mail.ResendKey(); k != "re_one" || err != nil {
		t.Fatalf("%q %v", k, err)
	}
	if err := os.WriteFile(p, []byte("re_two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, _ := c.Mail.ResendKey(); k != "re_two" {
		t.Fatalf("%q", k)
	}
	// an empty, missing or malformed one
	for _, bad := range []string{keyFile(t, " \n"), keyFile(t, "re_a b"), keyFile(t, "sk_live_1"), keyFile(t, "re_a\nX: y"), "/nonexistent/resend.key"} {
		c.Mail.APIKeyPath = bad
		if err := Validate(c, RoleAPI); err == nil || strings.Contains(err.Error(), "re_a") || err != nil && strings.Contains(err.Error(), "sk_live") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// the worker needs no key, but the token secret its outbox is sealed with
	c.Mail.APIKeyPath = ""
	if err := Validate(c, RoleWorker); !errors.Is(err, ErrMissing) {
		t.Errorf("worker: %v", err)
	}
	c.Auth.TokenSecret = strings.Repeat("s", 32)
	if err := Validate(c, RoleWorker); err != nil {
		t.Errorf("worker: %v", err)
	}
}

func TestMailKeyIsNeverInTheFile(t *testing.T) {
	for _, k := range []string{"api_key", "resend_api_key", "webhook_secret", "resend_webhook_secret"} {
		if _, err := mailConfig(t, k+" = \"re_x\"\n"); err == nil || strings.Contains(err.Error(), "re_x") {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func TestMailWebhookSecret(t *testing.T) {
	const good = "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	// Absent file is optional (returns ErrMissing; Validate passes).
	c, err := mailConfig(t, "resend_webhook_secret_path = \"/nonexistent/resend-webhook.key\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Mail.ResendWebhookSecret(); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing file: %v", err)
	}
	if err := Validate(c, RoleAPI); err != nil {
		t.Fatalf("missing optional webhook file refused: %v", err)
	}
	// File hot-reloads on rotation.
	p := keyFile(t, good+"\n")
	t.Setenv("MAKTABA_CLOUD_RESEND_WEBHOOK_SECRET_FILE", p)
	c, err = mailConfig(t, "")
	if err != nil || Validate(c, RoleAPI) != nil {
		t.Fatalf("%v / %v", err, Validate(c, RoleAPI))
	}
	if got, err := c.Mail.ResendWebhookSecret(); err != nil || got != good {
		t.Fatalf("%q %v", got, err)
	}
	const rotated = "whsec_ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
	if err := os.WriteFile(p, []byte(rotated), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Mail.ResendWebhookSecret(); err != nil || got != rotated {
		t.Fatalf("rotated: %q %v", got, err)
	}
	// Malformed secret is refused without echoing the secret.
	for _, bad := range []string{"bad_prefix", "whsec_not!base64", "whsec_a b"} {
		c.Mail.WebhookSecretPath = keyFile(t, bad)
		err := Validate(c, RoleAPI)
		if err == nil || strings.Contains(err.Error(), bad) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestMailRefusals(t *testing.T) {
	t.Setenv("MAKTABA_CLOUD_RESEND_API_KEY", "re_x")
	for name, change := range map[string]func(*Config){
		"another driver":        func(c *Config) { c.Mail.Driver = "smtp" },
		"no driver":             func(c *Config) { c.Mail.Driver = "" },
		"no from":               func(c *Config) { c.Mail.From = "" },
		"a bad from":            func(c *Config) { c.Mail.From = "no-reply" },
		"a bad reply_to":        func(c *Config) { c.Mail.ReplyTo = "x y" },
		"a bad recipient":       func(c *Config) { c.Mail.AllowRecipients = []string{"*"} },
		"no web app":            func(c *Config) { c.Server.WebURL = "" },
		"a web app over http":   func(c *Config) { c.Server.WebURL = "http://app.example.test" },
		"no daily limit":        func(c *Config) { c.Mail.DailyLimit = 0 },
		"a negative limit":      func(c *Config) { c.Mail.MonthlyLimit = -1 },
		"staging and strangers": func(c *Config) { c.Server.Env, c.Mail.AllowRecipients = "staging", nil },
	} {
		c, err := mailConfig(t, resendTOML)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(c, RoleAPI); err != nil {
			t.Fatal(err)
		}
		change(&c)
		if Validate(c, RoleAPI) == nil {
			t.Errorf("%s is accepted", name)
		}
	}
}
