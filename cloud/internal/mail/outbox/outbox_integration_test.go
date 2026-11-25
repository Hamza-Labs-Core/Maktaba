//go:build integration

package outbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

const secret = "an outbox test secret of 32 bytes!"

// mailer answers each send with the next of errs (nil past the end) and keeps what it was given.
type mailer struct {
	mu   sync.Mutex
	errs []error
	sent []mail.Message
	hook func(context.Context) error
}

func (m *mailer) Send(ctx context.Context, msg mail.Message) error {
	m.mu.Lock()
	n := len(m.sent)
	m.sent = append(m.sent, msg)
	hook := m.hook
	m.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	if n < len(m.errs) {
		return m.errs[n]
	}
	return nil
}

type world struct {
	t     *testing.T
	o     *Outbox
	m     *mailer
	users *stores.Users
	logs  *bytes.Buffer
	at    time.Time
}

func newWorld(t *testing.T, l Limits) *world {
	t.Helper()
	pool := dbtest.Migrated(t)
	w := &world{t: t, m: &mailer{}, users: stores.NewUsers(pool.DB), logs: &bytes.Buffer{}, at: time.Now().Truncate(time.Millisecond)}
	o, err := New(pool.DB, secret, w.m, mail.Templates{WebURL: "https://app.example.test"}, l, slog.New(slog.NewTextHandler(w.logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	o.now = func() time.Time { return w.at }
	w.o = o
	return w
}

// work resolves and sends until there is nothing more to do now.
func (w *world) work() {
	w.t.Helper()
	ctx := context.Background()
	for _, step := range []func(context.Context) (bool, error){w.o.Resolve, w.o.SendOne} {
		for {
			more, err := step(ctx)
			if err != nil {
				w.t.Fatal(err)
			}
			if !more {
				break
			}
		}
	}
}

func (w *world) states() string {
	w.t.Helper()
	c, err := Count(context.Background(), w.o.DB)
	if err != nil {
		w.t.Fatal(err)
	}
	return fmt.Sprint(c.States)
}

func (w *world) register(email, lang string) {
	w.t.Helper()
	if err := w.o.Register(context.Background(), mail.Ask{To: email, Lang: lang}, "hash", ""); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) ask(kind, email, lang string) {
	w.t.Helper()
	if err := w.o.Request(context.Background(), mail.Ask{Kind: kind, To: email, Lang: lang}); err != nil {
		w.t.Fatal(err)
	}
}

// verified is an account whose address is confirmed.
func (w *world) verified(email string) {
	w.t.Helper()
	if _, err := w.users.Create(context.Background(), email, "hash", "", true); err != nil {
		w.t.Fatal(err)
	}
}

// to is how many mails of kind went to an address ("" is any kind).
func (w *world) to(email, kind string) int {
	n := 0
	for _, m := range w.m.sent {
		if m.To == email && (kind == "" || m.Kind == kind) {
			n++
		}
	}
	return n
}

func token(m mail.Message) string {
	_, rest, _ := strings.Cut(m.Body, "token=")
	return strings.Fields(rest)[0]
}

func (w *world) last() mail.Message {
	w.t.Helper()
	if len(w.m.sent) == 0 {
		w.t.Fatal("no mail was sent")
	}
	return w.m.sent[len(w.m.sent)-1]
}

func TestASignUpIsMailedInItsLanguage(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.register("new@example.test", "ar")
	if len(w.m.sent) != 0 {
		t.Fatal("the request sent the mail itself")
	}
	w.work()
	m := w.last()
	if m.Kind != mail.KindVerifyEmail || m.To != "new@example.test" || m.Lang != "ar" || m.ID == "" || !strings.Contains(m.HTML, `dir="rtl"`) {
		t.Fatalf("%+v", m)
	}
	u, err := w.users.ByEmail(context.Background(), "new@example.test")
	if err != nil || u.Locale != "ar" || u.EmailVerified {
		t.Fatalf("%+v %v", u, err)
	}
	// the address again: its owner is told, in the account's language whatever the request says
	w.register("new@example.test", "en")
	w.work()
	if m := w.last(); m.Kind != mail.KindAlreadyRegistered || m.Lang != "ar" {
		t.Fatalf("%+v", m)
	}
	// a reset in the language asked for, and in the account's when none is
	for _, l := range []struct{ asked, want string }{{"en", "en"}, {"", "ar"}} {
		w.ask(mail.KindPasswordReset, "new@example.test", l.asked)
		w.work()
		if m := w.last(); m.Kind != mail.KindPasswordReset || m.Lang != l.want {
			t.Fatalf("%+v", m)
		}
		w.at = w.at.Add(2 * time.Hour)
	}
	// a mail's log lines name no address, link or subject
	for _, s := range []string{"new@example.test", "token=", m.Subject, "app.example.test"} {
		if strings.Contains(w.logs.String(), s) {
			t.Errorf("the log says %q", s)
		}
	}
}

// What a request writes is the same for an address with an account and one without.
func TestARequestWritesTheSameForAnyAddress(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.register("known@example.test", "en")
	w.work()
	type row struct {
		class, state, lang string
		sealed             int
		wait               time.Duration
	}
	one := func(do func()) (string, row) {
		t.Helper()
		if _, err := w.o.DB.Exec(`DELETE FROM mail_outbox`); err != nil {
			t.Fatal(err)
		}
		do()
		var r row
		var kind string
		var n int
		var created, expires time.Time
		if err := w.o.DB.QueryRow(`SELECT count(*) OVER (), kind, class, state, lang, length(sealed), created_at, expires_at FROM mail_outbox`).
			Scan(&n, &kind, &r.class, &r.state, &r.lang, &r.sealed, &created, &expires); err != nil || n != 1 {
			t.Fatalf("%d rows: %v", n, err)
		}
		r.wait = expires.Sub(created)
		return kind, r
	}
	for _, kind := range []string{mail.KindPasswordReset, mail.KindVerifyEmail} {
		_, known := one(func() { w.ask(kind, "known@example.test", "en") })
		_, ghost := one(func() { w.ask(kind, "ghost@example.test", "en") })
		if known != ghost {
			t.Errorf("%s: %+v, %+v", kind, known, ghost)
		}
	}
	k1, taken := one(func() { w.register("known@example.test", "") })
	k2, free := one(func() { w.register("fresh@example.test", "") })
	if taken != free || k1 != mail.KindAlreadyRegistered || k2 != mail.KindVerifyEmail {
		t.Errorf("a sign-up: %s %+v, %s %+v", k1, taken, k2, free)
	}
	// and an address without an account is mailed nothing
	n := len(w.m.sent)
	w.ask(mail.KindPasswordReset, "ghost@example.test", "en")
	w.ask(mail.KindVerifyEmail, "ghost@example.test", "en")
	if _, err := w.o.DB.Exec(`DELETE FROM mail_outbox WHERE kind = 'already_registered'`); err != nil {
		t.Fatal(err)
	}
	w.work()
	for _, m := range w.m.sent[n:] {
		if m.To == "ghost@example.test" {
			t.Errorf("mailed an unknown address: %+v", m)
		}
	}
}

// Nothing in the table opens without the key: no address, link or content.
func TestTheTableHoldsNothingInTheClear(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.register("private@example.test", "en")
	w.ask(mail.KindPasswordReset, "private@example.test", "en")
	dump := func() string {
		var s string
		if err := w.o.DB.QueryRow(`SELECT string_agg(m::text, E'\n') FROM mail_outbox m`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, s := range []string{"private", "token", "example.test"} {
		if strings.Contains(dump(), s) {
			t.Errorf("a queued row says %q", s)
		}
	}
	for range 2 {
		if _, err := w.o.Resolve(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if c, err := w.o.claim(context.Background()); err != nil || c == nil || c.msg == nil {
		t.Fatalf("%+v %v", c, err)
	}
	for _, s := range []string{"private", "token", "example.test"} {
		if strings.Contains(dump(), s) {
			t.Errorf("a written mail's row says %q", s)
		}
	}
	w.at = w.at.Add(5 * time.Minute)
	w.work()
	var sealed int
	if err := w.o.DB.QueryRow(`SELECT count(*) FROM mail_outbox WHERE sealed IS NOT NULL`).Scan(&sealed); err != nil || sealed != 0 {
		t.Fatalf("%d finished rows keep their content: %v", sealed, err)
	}
	// another key opens nothing, and nothing is sent
	other, err := New(w.o.DB, "another outbox secret of 32 bytes!!", w.m, w.o.Templates, Limits{}, w.o.Logger)
	if err != nil {
		t.Fatal(err)
	}
	n := len(w.m.sent)
	w.at = w.at.Add(2 * time.Hour)
	other.now = w.o.now
	w.ask(mail.KindPasswordReset, "private@example.test", "en")
	if _, err := other.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := other.SendOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.m.sent) != n || !strings.Contains(w.states(), "failed:1") {
		t.Fatalf("%s", w.states())
	}
	if _, err := New(w.o.DB, "short", w.m, w.o.Templates, Limits{}, w.o.Logger); err == nil {
		t.Error("an outbox without a secret")
	}
}

// A mail not taken is tried again, later each time, as the same mail (its id and its link).
func TestATemporaryFailureIsTriedAgain(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.m.errs = []error{&mail.SendError{Status: 503, Temporary: true}, errors.New("secret@example.test unreachable"), nil}
	w.register("a@example.test", "en")
	w.work()
	w.work() // not yet
	if len(w.m.sent) != 1 || !strings.Contains(w.states(), "ready:1") {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
	}
	w.at = w.at.Add(Backoff(1))
	w.work()
	c, err := Count(context.Background(), w.o.DB)
	if err != nil || c.LastErrors["no answer"] != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	w.at = w.at.Add(Backoff(2))
	w.work()
	if len(w.m.sent) != 3 || w.m.sent[0] != w.m.sent[1] || w.m.sent[1] != w.m.sent[2] || !strings.Contains(w.states(), "sent:1") {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
	}
	for i := 1; i < 12; i++ {
		if Backoff(i+1) < Backoff(i) || Backoff(i) <= 0 {
			t.Fatalf("Backoff(%d) = %s", i+1, Backoff(i+1))
		}
	}
}

func TestAPermanentFailureIsNot(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.m.errs = []error{&mail.SendError{Status: 422, Name: "validation_error"}}
	w.register("a@example.test", "en")
	w.work()
	w.at = w.at.Add(6 * time.Hour)
	w.work()
	if len(w.m.sent) != 1 || !strings.Contains(w.states(), "failed:1") {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
	}
}

// A refused key or a spent plan waits: the mail goes once it is set right, and the provider is
// not asked again and again.
func TestARefusalWaits(t *testing.T) {
	for _, tc := range []struct {
		err  *mail.SendError
		wait time.Duration
	}{
		{&mail.SendError{Status: 403, Name: "invalid_api_key", Temporary: true, Refused: true}, 5 * time.Minute},
		{&mail.SendError{Status: 429, Name: "daily_quota_exceeded", Temporary: true, Quota: true}, time.Hour},
		{&mail.SendError{Status: 429, Temporary: true, RetryAfter: 7 * time.Minute}, 7 * time.Minute},
	} {
		w := newWorld(t, DefaultLimits(100, 1000))
		w.m.errs = []error{tc.err}
		w.register("a@example.test", "en")
		w.work()
		w.at = w.at.Add(tc.wait - time.Second)
		w.work()
		if len(w.m.sent) != 1 {
			t.Fatalf("%v: asked again before %s", tc.err, tc.wait)
		}
		w.at = w.at.Add(2 * time.Second)
		w.work()
		if len(w.m.sent) != 2 || !strings.Contains(w.states(), "sent:1") {
			t.Fatalf("%v: %d sends, %s", tc.err, len(w.m.sent), w.states())
		}
	}
}

// A process that stops while it sends, or dies, loses no mail: the next one sends the same.
func TestAMailSurvivesARestart(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.register("a@example.test", "en")
	if _, err := w.o.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	// stopped in the middle of a send
	ctx, cancel := context.WithCancel(context.Background())
	w.m.hook = func(ctx context.Context) error { cancel(); <-ctx.Done(); return ctx.Err() }
	if _, err := w.o.SendOne(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.states(), "ready:1") {
		t.Fatal(w.states())
	}
	// killed in the middle of a send: the claim lapses
	w.m.hook = nil
	if c, err := w.o.claim(context.Background()); err != nil || c == nil {
		t.Fatalf("%v %v", c, err)
	}
	w.work()
	if len(w.m.sent) != 1 {
		t.Fatal("a mail being sent was sent by another")
	}
	w.at = w.at.Add(w.o.SendTimeout + time.Minute)
	w.work()
	if len(w.m.sent) != 2 || w.m.sent[0] != w.m.sent[1] || !strings.Contains(w.states(), "sent:1") {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
	}
}

func TestAnOldMailExpires(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.register("a@example.test", "en")
	w.work()
	w.m.errs = []error{nil, &mail.SendError{Status: 500, Temporary: true}}
	w.ask(mail.KindPasswordReset, "a@example.test", "en")
	w.work()
	// past half its link's life it would arrive nearly dead: not sent
	w.at = w.at.Add(stores.PasswordResetTTL/2 + time.Second)
	w.work()
	if err := w.o.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.m.sent) != 2 || !strings.Contains(w.states(), "expired:1") {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
	}
	w.at = w.at.Add(Retention + time.Hour)
	if err := w.o.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.states() != "map[]" {
		t.Fatal(w.states())
	}
}

// Whoever asked before, a confirmed person has a link that works, or is sent one within the hour,
// with the day's mails strangers and repeats may use all gone: the review's attack.
func TestAPersonAlwaysGetsAResetWithinTheHour(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, DefaultLimits(85, 2700))
	w.verified("victim@example.test")
	w.ask(mail.KindPasswordReset, "victim@example.test", "en")
	w.work()
	delivered, at := token(w.last()), w.at
	// 51 sign-ups, each with the bot check passed, and 20 repeats of another confirmed address
	for i := range 60 {
		if err := w.o.Register(ctx, mail.Ask{To: fmt.Sprintf("s%d@example.test", i), Checked: true}, "hash", ""); err != nil {
			t.Fatal(err)
		}
	}
	w.verified("other@example.test")
	for range 20 {
		w.ask(mail.KindPasswordReset, "other@example.test", "en")
		w.work()
		w.at = w.at.Add(15 * time.Minute)
	}
	// and then the victim, every two minutes, for 16 hours
	asks := w.at
	newest := at
	for w.at.Before(asks.Add(16 * time.Hour)) {
		n := len(w.m.sent)
		w.ask(mail.KindPasswordReset, "victim@example.test", "en")
		w.work()
		for _, m := range w.m.sent[n:] {
			if m.To == "victim@example.test" {
				newest, delivered = w.at, token(m)
			}
		}
		if waited := w.at.Sub(newest); newest != at && waited > time.Hour+4*time.Minute {
			t.Fatalf("%s without a reset, %s into it", waited, w.at.Sub(asks))
		}
		w.at = w.at.Add(2 * time.Minute)
	}
	if n := w.to("victim@example.test", mail.KindPasswordReset); n < 15 || n > 20 {
		t.Fatalf("%d resets in 16 hours", n)
	}
	if _, err := w.o.tokens.Redeem(ctx, delivered, stores.PurposePasswordReset); err != nil {
		t.Errorf("the newest delivered link: %v", err)
	}
}

// An ask that gets no mail ends no link: not one refused, not one with no room for it, and not one
// whose mail the provider does not take. Using one ends the others.
func TestAnAskNeverEndsADeliveredLink(t *testing.T) {
	ctx := context.Background()
	for name, then := range map[string]func(*world){
		"a moment later": func(w *world) { w.at = w.at.Add(time.Minute) },
		"no room":        func(w *world) { w.at = w.at.Add(20 * time.Minute); w.o.Limits.Day = 1 },
		"a 503": func(w *world) {
			w.at = w.at.Add(20 * time.Minute)
			w.m.hook = func(context.Context) error { return &mail.SendError{Status: 503, Temporary: true} }
		},
		"a delivered one": func(w *world) { w.at = w.at.Add(20 * time.Minute) },
	} {
		w := newWorld(t, DefaultLimits(100, 1000))
		w.verified("a@example.test")
		w.ask(mail.KindPasswordReset, "a@example.test", "en")
		w.work()
		link := token(w.last())
		then(w)
		for range 3 {
			w.ask(mail.KindPasswordReset, "a@example.test", "en")
			w.work()
		}
		later := token(w.last())
		if _, err := w.o.tokens.Redeem(ctx, link, stores.PurposePasswordReset); err != nil {
			t.Errorf("%s: the delivered link: %v", name, err)
		}
		if _, err := w.o.tokens.Redeem(ctx, later, stores.PurposePasswordReset); !errors.Is(err, stores.ErrTokenInvalid) {
			t.Errorf("%s: a link outlived the one that was used: %v", name, err)
		}
	}
}

// One address: a mail of a kind each so often, whatever is asked; another kind and another address
// are not touched; an ask nothing comes of keeps no address.
func TestTheLimitsOfOneAddress(t *testing.T) {
	w := newWorld(t, DefaultLimits(10000, 100000))
	w.register("a@example.test", "en")
	w.verified("b@example.test")
	w.work()
	// asked every five minutes for five hours
	for range 60 {
		for _, k := range []string{mail.KindPasswordReset, mail.KindVerifyEmail} {
			w.ask(k, "A@example.test", "en")
		}
		w.ask(mail.KindPasswordReset, "b@example.test", "en")
		w.register("b@example.test", "en")
		w.work()
		w.at = w.at.Add(5 * time.Minute)
	}
	for _, want := range []struct {
		to, kind string
		n        int
	}{
		{"a@example.test", mail.KindPasswordReset, 5},     // nobody proved it: one an hour
		{"a@example.test", mail.KindVerifyEmail, 6},       // one a quarter of an hour, six a day
		{"b@example.test", mail.KindPasswordReset, 20},    // one a quarter of an hour
		{"b@example.test", mail.KindAlreadyRegistered, 1}, // one in eight hours
	} {
		if n := w.to(want.to, want.kind); n != want.n {
			t.Errorf("%d %s to %s in five hours, want %d", n, want.kind, want.to, want.n)
		}
	}
	if !strings.Contains(w.logs.String(), "mail_address_limit") {
		t.Error("nothing says so")
	}
	var kept int
	if err := w.o.DB.QueryRow(`SELECT count(*) FROM mail_outbox WHERE state = 'dropped' AND (to_hash <> '' OR sealed IS NOT NULL)`).Scan(&kept); err != nil || kept != 0 {
		t.Errorf("%d asks nothing came of keep their address: %v", kept, err)
	}
	// a day later they, and every network, are gone; a mail sent still counts
	if err := w.o.Request(context.Background(), mail.Ask{Kind: mail.KindPasswordReset, To: "b@example.test", Net: "net:203.0.113.0/24"}); err != nil {
		t.Fatal(err)
	}
	w.work()
	nets := func() (n int) {
		t.Helper()
		if err := w.o.DB.QueryRow(`SELECT count(*) FROM mail_outbox WHERE net_hash <> ''`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, late := range []time.Duration{23 * time.Hour, 25 * time.Hour} {
		w.at = w.at.Add(late)
		if err := w.o.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := nets(); n != map[bool]int{true: 1, false: 0}[late < 24*time.Hour] {
			t.Errorf("%d networks kept %s on", n, late)
		}
	}
	if strings.Contains(w.states(), "dropped") {
		t.Error(w.states())
	}
}

// One network: ten asks in any hour and twenty in any day, forty and sixty with the bot check,
// counted as asked, whether or not there is an account; refused, it is told when to try again.
func TestTheLimitsOfOneNetwork(t *testing.T) {
	ctx := context.Background()
	for _, checked := range []bool{false, true} {
		l := DefaultLimits(85, 2700)
		w := newWorld(t, l)
		w.verified("known@example.test")
		win := l.Net
		if checked {
			win = l.NetChecked
		}
		start := w.at
		var granted []time.Time
		for i := range 36 * 3 { // each twenty minutes, for a day and a half, a burst at a time
			for j := range 14 {
				a := mail.Ask{Kind: mail.KindPasswordReset, To: "known@example.test", Net: "net:203.0.113.0/24", Checked: checked}
				if j%2 == 0 {
					a.To = fmt.Sprintf("ghost%d-%d@example.test", i, j)
				}
				var err error
				if j == 3 {
					err = w.o.Register(ctx, a, "hash", "")
				} else {
					err = w.o.Request(ctx, a)
				}
				var many *TooMany
				switch {
				case err == nil:
					granted = append(granted, w.at)
				case !errors.As(err, &many) || many.RetryAfter <= 0 || many.RetryAfter > 24*time.Hour:
					t.Fatalf("%v", err)
				}
			}
			w.at = w.at.Add(20 * time.Minute)
		}
		most := func(span time.Duration) int {
			n := 0
			for i := range granted {
				k := 0
				for _, g := range granted[i:] {
					if g.Sub(granted[i]) < span {
						k++
					}
				}
				n = max(n, k)
			}
			return n
		}
		if h, d := most(time.Hour), most(24*time.Hour); h != win.Hour || d != win.Day || len(granted) < 2*win.Day {
			t.Fatalf("checked %v: at most %d an hour and %d a day, %d in all; want %+v", checked, h, d, len(granted), win)
		}
		// the two kinds count apart, and another network is not touched
		for _, a := range []mail.Ask{{Net: "net:203.0.113.0/24", Checked: !checked}, {Net: "net:198.51.100.0/24", Checked: checked}} {
			a.Kind, a.To = mail.KindPasswordReset, "someone@example.test"
			if err := w.o.Request(ctx, a); err != nil {
				t.Errorf("%+v: %v", a, err)
			}
		}
		// told when: the wait it was given is enough
		w.at = start.Add(100 * 24 * time.Hour)
		var many *TooMany
		for !errors.As(w.o.Request(ctx, mail.Ask{Kind: mail.KindPasswordReset, To: "x@example.test", Net: "n", Checked: checked}), &many) {
			w.at = w.at.Add(time.Second)
		}
		w.at = w.at.Add(many.RetryAfter + time.Second)
		if err := w.o.Request(ctx, mail.Ask{Kind: mail.KindPasswordReset, To: "x@example.test", Net: "n", Checked: checked}); err != nil {
			t.Errorf("after the wait it was told: %v", err)
		}
		// a refused sign-up makes no account, and asks at once are still counted one at a time
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := range 60 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := w.o.Register(ctx, mail.Ask{To: fmt.Sprintf("burst%d@example.test", i), Net: "burst", Checked: checked}, "hash", "")
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					ok++
				}
			}()
		}
		wg.Wait()
		var accounts int
		if err := w.o.DB.QueryRow(`SELECT count(*) FROM users WHERE email LIKE 'burst%'`).Scan(&accounts); err != nil || ok != win.Hour || accounts != win.Hour {
			t.Fatalf("checked %v: %d of 60 at once, %d accounts, want %d: %v", checked, ok, accounts, win.Hour, err)
		}
	}
}

// Strangers use their share of the day and no more, smaller without the bot check; the repeats of
// confirmed accounts theirs; and the first reset of an hour and a notice go at once. At
// production's limits, with the second review's own: its sign-ups, then resets of confirmed
// accounts of the attacker's own, all from one network or two.
func TestStrangersCannotUseUpTheDay(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, DefaultLimits(85, 2700))
	for i := range 12 {
		w.verified(fmt.Sprintf("mine%d@example.test", i))
	}
	w.verified("owner@example.test")
	attack := func(nets int) {
		for h := range 24 {
			for n := range nets {
				for i := range 12 {
					a := mail.Ask{Kind: mail.KindPasswordReset, To: fmt.Sprintf("mine%d@example.test", (i+h)%12), Net: fmt.Sprintf("net%d", n)}
					if i%2 == 0 {
						a.To = fmt.Sprintf("s%d-%d-%d@example.test", h, n, i)
						_ = w.o.Register(ctx, a, "hash", "")
					} else {
						_ = w.o.Request(ctx, a)
					}
				}
			}
			w.work()
			w.at = w.at.Add(time.Hour)
		}
	}
	before := len(w.m.sent)
	attack(1)
	if n := len(w.m.sent) - before; n > 20 {
		t.Fatalf("one network had %d of the day's 85", n)
	}
	w.at = w.at.Add(48 * time.Hour)
	before = len(w.m.sent)
	attack(2)
	if n := len(w.m.sent) - before; n > 40 {
		t.Fatalf("two networks had %d of the day's 85", n)
	}
	// many networks: strangers without the bot check stop at 40 percent, with it at 60
	w.at = w.at.Add(48 * time.Hour)
	before = len(w.m.sent)
	for i := range 200 {
		w.register(fmt.Sprintf("x%d@example.test", i), "en")
	}
	w.work()
	if n := len(w.m.sent) - before; n != 34 {
		t.Fatalf("%d strangers' mails without the bot check, want 34", n)
	}
	for i := range 200 {
		if err := w.o.Register(ctx, mail.Ask{To: fmt.Sprintf("y%d@example.test", i), Checked: true}, "hash", ""); err != nil {
			t.Fatal(err)
		}
	}
	w.work()
	if n := len(w.m.sent) - before; n != 51 {
		t.Fatalf("%d strangers' mails, want 51", n)
	}
	// resets of accounts never confirmed are strangers' too: none goes
	for i := range 40 {
		w.ask(mail.KindPasswordReset, fmt.Sprintf("y%d@example.test", i), "en")
	}
	w.work()
	if n := len(w.m.sent) - before; n != 51 {
		t.Fatalf("%d strangers' mails, want 51", n)
	}
	// an account nobody has asked for, and a notice, go at once
	w.ask(mail.KindPasswordReset, "owner@example.test", "en")
	if err := w.o.Send(ctx, mail.Message{To: "owner@example.test", Kind: "payment_failed"}); err != nil {
		t.Fatal(err)
	}
	w.work()
	if w.to("owner@example.test", "") != 2 {
		t.Fatalf("a confirmed account's mails wait: %s", w.states())
	}
	if !strings.Contains(w.logs.String(), "mail_limit_reached") {
		t.Error("nothing says mails wait")
	}
}

// Repeats stop at their share, and the reserve past it is for the first of an hour and notices.
func TestRepeatsLeaveTheReserve(t *testing.T) {
	w := newWorld(t, DefaultLimits(20, 1000))
	w.verified("k@example.test")
	w.verified("owner@example.test")
	for i := range 20 {
		if err := w.o.Register(context.Background(), mail.Ask{To: fmt.Sprintf("s%d@example.test", i), Checked: true}, "hash", ""); err != nil {
			t.Fatal(err)
		}
	}
	w.work() // twelve strangers, 60 percent
	for range 4 {
		w.ask(mail.KindPasswordReset, "k@example.test", "en")
		w.work()
		w.at = w.at.Add(15 * time.Minute)
	}
	// the hour's first, and the two repeats to 70 percent
	if n := w.to("k@example.test", ""); n != 3 {
		t.Fatalf("%d resets of one account in an hour, want 3", n)
	}
	w.ask(mail.KindPasswordReset, "owner@example.test", "en")
	if err := w.o.Send(context.Background(), mail.Message{To: "owner@example.test", Kind: "payment_failed"}); err != nil {
		t.Fatal(err)
	}
	w.work()
	if w.to("owner@example.test", "") != 2 {
		t.Fatalf("a confirmed account's mails wait: %s", w.states())
	}
	// the one that waited becomes an hour's first
	w.at = w.at.Add(31 * time.Minute)
	w.work()
	if n := w.to("k@example.test", ""); n != 4 {
		t.Fatalf("%d resets, want the fourth once the hour passed", n)
	}
}

// What goes first: confirmed accounts' mails, then strangers', whoever asked first.
func TestConfirmedAccountsGoFirst(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.verified("known@example.test")
	w.register("stranger@example.test", "en")
	if _, err := w.o.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.at = w.at.Add(time.Minute)
	w.ask(mail.KindPasswordReset, "known@example.test", "en")
	w.work()
	if len(w.m.sent) != 2 || w.m.sent[0].To != "known@example.test" {
		t.Fatalf("%+v", w.m.sent)
	}
}

// The limits are counted and taken one mail at a time, whatever claims at once.
func TestClaimsAtOnceKeepTheLimit(t *testing.T) {
	w := newWorld(t, Limits{Day: 5})
	for i := range 40 {
		if err := w.o.Send(context.Background(), mail.Message{To: fmt.Sprintf("u%d@example.test", i), Kind: "payment_failed"}); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 { // a few rounds: without the lock some round goes over
		if _, err := w.o.DB.Exec(`UPDATE mail_outbox SET state = 'ready', lease_until = NULL WHERE state = 'sending'`); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		got := 0
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, err := w.o.claim(context.Background())
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					t.Error(err)
				} else if c != nil {
					got++
				}
			}()
		}
		wg.Wait()
		if got != 5 {
			t.Fatalf("%d claimed of a day of 5", got)
		}
	}
}

// A suspended or erased account, and one confirmed meanwhile, is sent nothing, whether its mail
// was still a request or waited written.
func TestAnAccountThatNoLongerWantsItsMail(t *testing.T) {
	ctx := context.Background()
	suspend := func(w *world, email string) {
		t.Helper()
		if _, err := w.o.DB.Exec(`UPDATE users SET status = 'suspended' WHERE email = $1`, email); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{mail.KindPasswordReset, mail.KindVerifyEmail, mail.KindAlreadyRegistered} {
		for _, written := range []bool{false, true} {
			for name, change := range map[string]func(*world){
				"suspended": func(w *world) { suspend(w, "a@example.test") },
				"gone": func(w *world) {
					if _, err := w.o.DB.Exec(`DELETE FROM users`); err != nil {
						t.Fatal(err)
					}
				},
			} {
				w := newWorld(t, DefaultLimits(100, 1000))
				w.register("a@example.test", "en")
				w.work()
				w.m.sent = nil
				w.at = w.at.Add(9 * time.Hour)
				w.ask(kind, "a@example.test", "en")
				if written {
					if _, err := w.o.Resolve(ctx); err != nil {
						t.Fatal(err)
					}
				}
				change(w)
				w.work()
				if len(w.m.sent) != 0 {
					t.Errorf("%s, %s, written %v: %+v", kind, name, written, w.m.sent)
				}
			}
		}
	}
	// a confirmation of an account confirmed already
	for _, written := range []bool{false, true} {
		w := newWorld(t, DefaultLimits(100, 1000))
		w.register("a@example.test", "en")
		if written {
			if _, err := w.o.Resolve(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.o.DB.Exec(`UPDATE users SET email_verified = true`); err != nil {
			t.Fatal(err)
		}
		w.work()
		if len(w.m.sent) != 0 {
			t.Errorf("written %v: a confirmed account was asked to confirm", written)
		}
	}
	// an erased account's address leaves the outbox; what was sent still counts
	w := newWorld(t, DefaultLimits(100, 1000))
	w.register("a@example.test", "en")
	w.work()
	w.ask(mail.KindPasswordReset, "a@example.test", "en")
	if err := w.o.Forget(ctx, "A@example.test"); err != nil {
		t.Fatal(err)
	}
	w.work()
	var n int
	if err := w.o.DB.QueryRow(`SELECT count(*) FROM mail_outbox WHERE to_hash = $1`, w.o.toHash("a@example.test")).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d rows still say the address: %v", n, err)
	}
	if len(w.m.sent) != 1 || w.states() != "map[sent:1]" {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
	}
}

// What the log driver took was not sent, and does not count once mails are.
func TestLoggedMailsDoNotCount(t *testing.T) {
	w := newWorld(t, Limits{Day: 3})
	w.o.Logs = true
	for i := range 3 {
		if err := w.o.Send(context.Background(), mail.Message{To: fmt.Sprintf("u%d@example.test", i), Kind: "payment_failed"}); err != nil {
			t.Fatal(err)
		}
	}
	w.work()
	if w.states() != "map[logged:3]" {
		t.Fatal(w.states())
	}
	w.o.Logs = false
	if err := w.o.Send(context.Background(), mail.Message{To: "u9@example.test", Kind: "payment_failed"}); err != nil {
		t.Fatal(err)
	}
	w.work()
	if c, err := Count(context.Background(), w.o.DB); err != nil || c.Sent24h != 1 || c.States[StateLogged] != 3 {
		t.Fatalf("%+v %v", c, err)
	}
}

// No mail is tried again once the provider may have forgotten its idempotency key (24 h).
func TestNoMailOutlivesItsIdempotencyKey(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.m.hook = func(context.Context) error { return &mail.SendError{Temporary: true} }
	w.register("a@example.test", "en")
	if err := w.o.Send(context.Background(), mail.Message{To: "b@example.test", Kind: "payment_failed"}); err != nil {
		t.Fatal(err)
	}
	first, last := map[string]time.Time{}, map[string]time.Time{}
	for range 200 {
		n := len(w.m.sent)
		w.work()
		for _, m := range w.m.sent[n:] {
			if _, ok := first[m.ID]; !ok {
				first[m.ID] = w.at
			}
			last[m.ID] = w.at
		}
		w.at = w.at.Add(30 * time.Minute)
	}
	for id := range first {
		if d := last[id].Sub(first[id]); d >= 23*time.Hour || d < 11*time.Hour {
			t.Errorf("tried for %s", d)
		}
	}
	if len(first) != 2 {
		t.Fatalf("%d mails", len(first))
	}
}

func TestTheLimitOfTheMonth(t *testing.T) {
	w := newWorld(t, Limits{Day: 100, Month: 3})
	for i := range 6 {
		if err := w.o.Send(context.Background(), mail.Message{To: fmt.Sprintf("u%d@example.test", i), Kind: "payment_failed"}); err != nil {
			t.Fatal(err)
		}
	}
	w.work()
	w.at = w.at.Add(48 * time.Hour)
	w.work()
	if len(w.m.sent) != 3 {
		t.Fatalf("%d mails of a month of 3", len(w.m.sent))
	}
}

// Two nodes on one database send each mail once, within the one limit.
func TestTwoNodesShareTheOutbox(t *testing.T) {
	w := newWorld(t, Limits{Day: 12})
	w.o.now = time.Now
	for i := range 20 {
		if err := w.o.Send(context.Background(), mail.Message{To: fmt.Sprintf("u%d@example.test", i), Kind: "payment_failed"}); err != nil {
			t.Fatal(err)
		}
	}
	w.m.hook = func(context.Context) error { time.Sleep(5 * time.Millisecond); return nil }
	other, err := New(w.o.DB, secret, w.m, w.o.Templates, w.o.Limits, w.o.Logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, o := range []*Outbox{w.o, other} {
		o.Logger = slog.New(slog.DiscardHandler)
		wg.Add(1)
		go func() { defer wg.Done(); o.Run(ctx) }()
	}
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(w.states(), "sent:12") {
			break
		}
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	wg.Wait()
	seen := map[string]bool{}
	for _, m := range w.m.sent {
		if seen[m.ID] {
			t.Errorf("%s was sent twice", m.ID)
		}
		seen[m.ID] = true
	}
	if len(seen) != 12 {
		t.Fatalf("%d mails of a day of 12: %s", len(seen), w.states())
	}
}

// QA: a mail to a stranger is written and given up, never handed over.
func TestAMailOffTheListIsSuppressed(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.o.Mailer = mail.Guard{Next: w.m, Allow: []string{"@maktaba.tv"}, Prefix: "[QA]"}
	w.register("ci-e2e-1@example.com", "en")
	w.register("team@maktaba.tv", "en")
	w.work()
	if len(w.m.sent) != 1 || w.last().To != "team@maktaba.tv" || !strings.HasPrefix(w.last().Subject, "[QA] ") ||
		!strings.Contains(w.states(), "suppressed:1") {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
	}
}

func TestAFullQueueDropsRequestsAndKeepsTheAccount(t *testing.T) {
	w := newWorld(t, Limits{})
	if _, err := w.o.DB.Exec(`INSERT INTO mail_outbox (id, kind, class, to_hash, state, expires_at)
        SELECT gen_random_uuid(), 'x', 'notice', 'h', 'queued', now() + INTERVAL '1 hour' FROM generate_series(1, $1)`, MaxQueued); err != nil {
		t.Fatal(err)
	}
	w.register("late@example.test", "en")
	if _, err := w.users.ByEmail(context.Background(), "late@example.test"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.o.DB.QueryRow(`SELECT count(*) FROM mail_outbox`).Scan(&n); err != nil || n != MaxQueued {
		t.Fatalf("%d rows: %v", n, err)
	}
}
