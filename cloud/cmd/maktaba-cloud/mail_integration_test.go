//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
)

// hangingMailer is a provider that never answers.
type hangingMailer struct{}

func (hangingMailer) Send(ctx context.Context, _ mailpkg.Message) error {
	<-ctx.Done()
	return ctx.Err()
}

// With a provider that never answers, registering, resending and resetting answer at once, the
// same for an address with an account as for one without: status, headers, body and time.
func TestMailNeitherBlocksNorTells(t *testing.T) {
	e := newEnv(t)
	const known = "known@example.test"
	e.registerVerified(known)
	o := e.o
	o.Mailer = hangingMailer{}
	a, stop, err := newAPI(e.logger, e.cfg, e.pool, e.mig, server.BuildInfo{Role: "api"}, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	e.h = consoleOrUser(a)

	type answer struct {
		code   int
		body   string
		header string
	}
	ask := func(path string, body map[string]string) (answer, time.Duration) {
		c := e.client() // its own address: not the per-IP limit's test
		start := time.Now()
		r := c.do("POST", path, body)
		took := time.Since(start)
		r.header.Del("X-Request-Id")
		return answer{r.code, string(r.body), fmt.Sprint(r.header)}, took
	}
	for _, p := range []string{"/v1/auth/password/forgot", "/v1/auth/verify-email/resend", "/v1/auth/register"} {
		best := map[bool]time.Duration{}
		var answers [2]answer
		for i := range 10 {
			for _, isKnown := range []bool{true, false} {
				body := map[string]string{"email": known, "password": goodPassword}
				if !isKnown {
					body["email"] = fmt.Sprintf("ghost%d-%d@example.test", i, len(p))
				}
				if p == "/v1/auth/register" && !isKnown {
					body["email"] = fmt.Sprintf("fresh%d@example.test", i)
				}
				ans, took := ask(p, body)
				if ans.code != http.StatusAccepted {
					t.Fatalf("%s: %+v", p, ans)
				}
				if took > 5*time.Second {
					t.Fatalf("%s waited %s for the mail", p, took)
				}
				if b, ok := best[isKnown]; !ok || took < b {
					best[isKnown] = took
				}
				n := 0
				if isKnown {
					n = 1
				}
				answers[n] = ans
			}
		}
		if answers[0] != answers[1] {
			t.Errorf("%s answers differ: %+v, %+v", p, answers[0], answers[1])
		}
		// the fastest of each: noise only ever adds time
		fast, slow := min(best[true], best[false]), max(best[true], best[false])
		if slow > 2*fast+15*time.Millisecond {
			t.Errorf("%s: known %s, unknown %s", p, best[true], best[false])
		}
	}
}

// The mail a sign-up causes is written in the app's language, and the account keeps it.
func TestMailFollowsTheRequestsLanguage(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.header = map[string]string{"Accept-Language": "ar"}
	c.mustStatus(c.do("POST", "/v1/auth/register", map[string]string{"email": "noor@example.test", "password": goodPassword}), http.StatusAccepted)
	m, ok := e.lastMail("verify_email", "noor@example.test")
	if !ok || m.Lang != "ar" || !strings.Contains(m.HTML, `dir="rtl"`) || !strings.Contains(m.Body, testWebURL+"/verify-email?token=") {
		t.Fatalf("%+v", m)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE email = 'noor@example.test' AND locale = 'ar'`); n != 1 {
		t.Fatal("the account's language is not the sign-up's")
	}
}

// /readyz says how mails go, and never the key.
func TestReadyzSaysHowMailGoes(t *testing.T) {
	get := func(e *env) map[string]any {
		r := e.client().do("GET", "/readyz", nil)
		if r.code != 200 {
			t.Fatalf("%d %s", r.code, r.body)
		}
		if strings.Contains(string(r.body)+e.logs.String(), "re_secret") {
			t.Fatalf("the key is told: %s", r.body)
		}
		m, _ := r.json(t)["mail"].(map[string]any)
		return m
	}
	if m := get(newEnv(t)); m["driver"] != "log" || m["delivers"] != false || m["recipients"] != "all" {
		t.Fatalf("%v", m)
	}
	key := filepath.Join(t.TempDir(), "resend.key")
	if err := os.WriteFile(key, []byte("re_secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Mail = config.MailConfig{Driver: "resend", From: "no-reply@example.test", APIKeyPath: key,
			AllowRecipients: []string{"@example.test"}, DailyLimit: 5, MonthlyLimit: 50}
	})
	if m := get(e); m["driver"] != "resend" || m["delivers"] != true || m["recipients"] != "listed" {
		t.Fatalf("%v", m)
	}
}

// A deployment that really sends: one network's sign-ups, confirmations and resets together are ten
// an hour, refused the same whether or not an address has an account, and told when to come back.
// What is not an ask (a weak password) is not counted, and another network is not touched.
func TestOneNetworkIsOneRequester(t *testing.T) {
	key := filepath.Join(t.TempDir(), "resend.key")
	if err := os.WriteFile(key, []byte("re_secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Mail = config.MailConfig{Driver: "resend", From: "no-reply@example.test", APIKeyPath: key, DailyLimit: 85, MonthlyLimit: 2700}
	})
	ask := func(ip string, i int, pw string) resp {
		c := e.client()
		c.ip = ip
		path := []string{"/v1/auth/register", "/v1/auth/password/forgot", "/v1/auth/verify-email/resend"}[i%3]
		return c.do("POST", path, map[string]string{"email": fmt.Sprintf("x%d-%s@example.test", i, strings.NewReplacer(":", "-", "[", "", "]", "").Replace(ip)), "password": pw})
	}
	for name, at := range map[string]func(i int) string{
		"IPv4": func(i int) string { return fmt.Sprintf("203.0.113.%d", 1+i) },
		"IPv6": func(i int) string { return fmt.Sprintf("[2001:db8:0:12%02x::%x]", i, 1+i) },
	} {
		for i := range 4 {
			if r := ask(at(100+i), 0, "short"); r.code != 400 {
				t.Fatalf("a weak password: %d", r.code)
			}
		}
		for i := range 13 {
			r := ask(at(i), i, goodPassword)
			if want := map[bool]int{true: 202, false: 429}[i < 10]; r.code != want {
				t.Fatalf("%s: request %d answers %d, want %d", name, i+1, r.code, want)
			}
			if r.code == 429 && (r.errCode() != "rate_limited" || len(r.header.Get("Retry-After")) != 4) {
				t.Fatalf("%s: %s, Retry-After %q", name, r.body, r.header.Get("Retry-After"))
			}
		}
	}
	if r := ask("198.51.100.7", 0, goodPassword); r.code != 202 {
		t.Fatalf("another network: %d", r.code)
	}
	if e.count(`SELECT count(*) FROM mail_outbox WHERE net_hash LIKE '%203.0.113%'`) != 0 {
		t.Fatal("a network in the clear")
	}
}

// A password change and signing out everywhere end the reset links in an inbox.
func TestAPasswordChangeEndsTheResetLinks(t *testing.T) {
	e := newEnv(t)
	const email = "someone@example.test"
	c := e.registerVerified(email)
	link := func() string {
		t.Helper()
		c.mustStatus(e.client().do("POST", "/v1/auth/password/forgot", map[string]string{"email": email}), http.StatusAccepted)
		return e.mailedToken("password_reset", email)
	}
	dead := func(when, tok string) {
		t.Helper()
		r := e.client().do("POST", "/v1/auth/password/reset", map[string]string{"token": tok, "password": "yet another passphrase"})
		if r.code != 400 || r.errCode() != "invalid_token" {
			t.Fatalf("after %s: %d %s", when, r.code, r.body)
		}
	}
	tok := link()
	c.mustStatus(c.do("POST", "/v1/account/password", map[string]string{"current_password": goodPassword, "new_password": "a brand new passphrase"}), http.StatusNoContent)
	dead("a password change", tok)
	c.login(email, "a brand new passphrase")
	tok = link()
	c.mustStatus(c.do("POST", "/v1/account/sessions/revoke-all", nil), http.StatusNoContent)
	dead("signing out everywhere", tok)
}

// The worker's account purge takes the address out of the outbox.
func TestThePurgeForgetsTheAccountsMail(t *testing.T) {
	e := newEnv(t)
	e.registerVerified("gone@example.test")
	e.lastMail("verify_email", "gone@example.test")
	if n := e.count(`SELECT count(*) FROM mail_outbox WHERE to_hash <> ''`); n == 0 {
		t.Fatal("no mail to forget")
	}
	e.exec(`INSERT INTO account_deletions (user_id, requested_at, purge_after) SELECT id, now() - INTERVAL '31 days', now() - INTERVAL '1 day' FROM users`)
	res, err := accountPurger(e.cfg, e.pool, e.logger).PurgeDue(context.Background())
	if err != nil || res.Purged != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if n := e.count(`SELECT count(*) FROM mail_outbox WHERE to_hash <> ''`); n != 0 {
		t.Fatalf("%d rows still hold the address", n)
	}
	// without the token secret there is no outbox to look in: said, not passed over in silence
	cfg := e.cfg
	cfg.Auth.TokenSecret = ""
	if p := accountPurger(cfg, e.pool, e.logger); p.ForgetMail != nil || !strings.Contains(e.logs.String(), "mail_forget_off") {
		t.Fatal("nothing says the outbox is not cleaned")
	}
}

// The operator's commands: a status without a key or an address, and a test mail through the
// running api role.
func TestMailCommands(t *testing.T) {
	e := newEnv(t)
	e.registerVerified("someone@example.test")
	var out bytes.Buffer
	c := mailCmd{Cfg: e.cfg, Pool: e.pool, Out: &out, In: strings.NewReader("team@example.test\n"), Wait: 20 * time.Second}
	if err := c.test(context.Background(), "ar"); err != nil || !strings.Contains(out.String(), "nothing is delivered") {
		t.Fatalf("%v: %s", err, out.String())
	}
	if m, ok := e.lastMail("test", "team@example.test"); !ok || m.Lang != "ar" {
		t.Fatalf("%+v", m)
	}
	if err := c.test(context.Background(), "fr"); err == nil {
		t.Fatal("a test mail in no language")
	}
	c.In = strings.NewReader("\n")
	if err := c.test(context.Background(), "en"); err == nil {
		t.Fatal("a test mail to nobody")
	}
	out.Reset()
	if err := c.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	var st struct {
		Driver   string `json:"driver"`
		Delivers bool   `json:"delivers"`
		Mails    struct {
			States map[string]int `json:"states"`
			Sent   int            `json:"sent_24h"`
		} `json:"mails"`
	}
	if err := json.Unmarshal(out.Bytes(), &st); err != nil || st.Driver != "log" || st.Delivers || st.Mails.States["logged"] != 2 || st.Mails.Sent != 0 {
		t.Fatalf("%v: %s", err, out.String())
	}
	if strings.Contains(out.String(), "@") {
		t.Fatalf("the status names an address: %s", out.String())
	}
}
