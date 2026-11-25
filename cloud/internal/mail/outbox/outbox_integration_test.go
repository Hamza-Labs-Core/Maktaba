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
	if err := w.o.Register(context.Background(), email, "hash", "", lang); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) ask(kind, email, lang string) {
	w.t.Helper()
	if err := w.o.Request(context.Background(), kind, email, lang); err != nil {
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
	if _, err := w.o.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"private", "token", "example.test"} {
		if strings.Contains(dump(), s) {
			t.Errorf("a written mail's row says %q", s)
		}
	}
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
	// past half the link's life it would arrive nearly dead: not sent
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

// Whoever asked before, a person can get a reset they can use: one refused for its rate leaves
// the newest link, good for at least 45 minutes more, in the inbox.
func TestAPersonAlwaysGetsAResetWithinTheHour(t *testing.T) {
	w := newWorld(t, DefaultLimits(1000, 10000))
	w.verified("owner@example.test")
	ctx := context.Background()
	// a stranger asks every two minutes, for half a day
	var newest time.Time
	for i := range 12 * 30 {
		before := len(w.m.sent)
		w.ask(mail.KindPasswordReset, "owner@example.test", "en")
		w.work()
		if len(w.m.sent) > before {
			newest = w.at
		} else if left := newest.Add(stores.PasswordResetTTL).Sub(w.at); left < 45*time.Minute {
			t.Fatalf("after %d asks: refused with %s left of the newest link", i+1, left)
		}
		w.at = w.at.Add(2 * time.Minute)
	}
	if n := len(w.m.sent); n > 12*5 || n < 12*3 {
		t.Fatalf("%d resets in half a day", n)
	}
	// the newest one works, and only it
	if _, err := w.o.tokens.Redeem(ctx, token(w.m.sent[len(w.m.sent)-2]), stores.PurposePasswordReset); err == nil {
		t.Error("an older link works")
	}
	if _, err := w.o.tokens.Redeem(ctx, token(w.last()), stores.PurposePasswordReset); err != nil {
		t.Errorf("the newest link: %v", err)
	}
	// the review's own: ten asks at once, then the owner, each of the next 20 hours
	w = newWorld(t, DefaultLimits(1000, 10000))
	w.verified("owner@example.test")
	for range 10 {
		w.ask(mail.KindPasswordReset, "owner@example.test", "en")
		w.work()
	}
	if len(w.m.sent) != 3 {
		t.Fatalf("%d of ten at once, want 3", len(w.m.sent))
	}
	for h := range 20 {
		w.at = w.at.Add(time.Hour)
		w.ask(mail.KindPasswordReset, "owner@example.test", "en")
		w.work()
		if len(w.m.sent) != 4+h {
			t.Fatalf("hour %d: no reset", h+1)
		}
	}
}

// An unproved address gets fewer; a refused request leaves no row; another kind and another
// address are not touched.
func TestTheLimitsOfOneAddress(t *testing.T) {
	w := newWorld(t, DefaultLimits(1000, 10000))
	w.register("a@example.test", "en")
	w.verified("b@example.test")
	w.work()
	for range 300 {
		w.ask(mail.KindPasswordReset, "A@example.test", "en")
		w.work()
	}
	if n := w.to("a@example.test", mail.KindPasswordReset); n != 2 {
		t.Fatalf("%d resets to an address nobody proved, want 2", n)
	}
	var rows int
	if err := w.o.DB.QueryRow(`SELECT count(*) FROM mail_outbox WHERE kind = 'password_reset'`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("%d rows of 300 asks: %v", rows, err)
	}
	if !strings.Contains(w.logs.String(), "mail_address_limit") {
		t.Error("nothing says so")
	}
	if _, err := w.o.tokens.Redeem(context.Background(), token(w.last()), stores.PurposePasswordReset); err != nil {
		t.Errorf("the last link it got is dead: %v", err)
	}
	w.ask(mail.KindPasswordReset, "b@example.test", "en")
	w.work()
	if w.to("b@example.test", mail.KindPasswordReset) != 1 {
		t.Fatal("another address pays for it")
	}
	// confirmations: three at once, then one each quarter of an hour, six a day
	for range 60 {
		w.ask(mail.KindVerifyEmail, "a@example.test", "en")
		w.work()
		w.at = w.at.Add(10 * time.Minute)
	}
	if n := w.to("a@example.test", mail.KindVerifyEmail); n != 6 {
		t.Fatalf("%d confirmations in ten hours, want 6", n)
	}
	// the notice to a taken address, which anyone can cause
	for range 4 {
		w.register("b@example.test", "en")
		w.work()
	}
	if n := w.to("b@example.test", mail.KindAlreadyRegistered); n != 1 {
		t.Errorf("%d notices of a taken address in an hour", n)
	}
	// notices have their own share
	over := 0
	for range 25 {
		if _, err := w.o.Keep(context.Background(), mail.Message{To: "c@example.test", Kind: "payment_failed"}); errors.Is(err, ErrLimited) {
			over++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if over != 5 {
		t.Errorf("%d notices refused, want 5", over)
	}
}

// The review's attack: strangers' addresses signed up, and resets asked for each. They use their
// share of the day, and no reset of an account whose address is confirmed ever waits for them.
func TestStrangersCannotUseUpTheDay(t *testing.T) {
	w := newWorld(t, DefaultLimits(90, 2700))
	for i := range 4 {
		w.verified(fmt.Sprintf("known%d@example.test", i))
	}
	for i := range 40 {
		w.register(fmt.Sprintf("stranger%d@example.test", i), "en")
	}
	w.work()
	for range 5 {
		for i := range 40 {
			w.ask(mail.KindPasswordReset, fmt.Sprintf("stranger%d@example.test", i), "en")
		}
		w.work()
		w.at = w.at.Add(20 * time.Minute)
	}
	if n := len(w.m.sent); n != 54 {
		t.Fatalf("%d mails to addresses nobody proved, want the 54 of their share", n)
	}
	// and known addresses asked for all day, by someone else
	for range 40 {
		for i := range 4 {
			w.ask(mail.KindPasswordReset, fmt.Sprintf("known%d@example.test", i), "en")
		}
		w.work()
		w.at = w.at.Add(16 * time.Minute)
	}
	if n := len(w.m.sent); n > 54+4*resetsFirst+18 || n <= 54+4*resetsFirst {
		t.Fatalf("%d mails of 90", n)
	}
	// an account nobody has asked for yet, and a notice, go at once
	w.verified("owner@example.test")
	w.ask(mail.KindPasswordReset, "owner@example.test", "en")
	if err := w.o.Send(context.Background(), mail.Message{To: "owner@example.test", Kind: "payment_failed"}); err != nil {
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

// What goes first: confirmed accounts' mails, then their repeats, then strangers'.
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
				w.at = w.at.Add(2 * time.Hour)
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

// A mail being sent when a newer one of its kind is written, and not taken, is not sent after it:
// its link is dead. The same when its process died.
func TestAnOlderLinkIsNeverTheLastToArrive(t *testing.T) {
	ctx := context.Background()
	for _, died := range []bool{false, true} {
		w := newWorld(t, DefaultLimits(100, 1000))
		w.verified("a@example.test")
		w.ask(mail.KindPasswordReset, "a@example.test", "en")
		if _, err := w.o.Resolve(ctx); err != nil {
			t.Fatal(err)
		}
		if died {
			if c, err := w.o.claim(ctx); err != nil || c == nil {
				t.Fatalf("%v %v", c, err)
			}
			w.at = w.at.Add(time.Second)
			w.ask(mail.KindPasswordReset, "a@example.test", "en")
			if _, err := w.o.Resolve(ctx); err != nil {
				t.Fatal(err)
			}
		} else {
			w.m.hook = func(context.Context) error {
				w.m.hook = nil
				w.at = w.at.Add(time.Second)
				w.ask(mail.KindPasswordReset, "a@example.test", "en")
				if _, err := w.o.Resolve(ctx); err != nil {
					t.Error(err)
				}
				return &mail.SendError{Status: 503, Temporary: true}
			}
			if _, err := w.o.SendOne(ctx); err != nil {
				t.Fatal(err)
			}
		}
		for range 4 {
			w.at = w.at.Add(5 * time.Minute)
			w.work()
		}
		if _, err := w.o.tokens.Redeem(ctx, token(w.last()), stores.PurposePasswordReset); err != nil {
			t.Errorf("died %v: the last link to arrive is dead", died)
		}
		if !strings.Contains(w.states(), "superseded:1") || !strings.Contains(w.states(), "sent:1") {
			t.Errorf("died %v: %s", died, w.states())
		}
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
	w := newWorld(t, DefaultLimits(100, 3))
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

// A newer request of a kind takes the place of an unsent mail, whose link is dead by then.
func TestANewerMailTakesAnOlderOnesPlace(t *testing.T) {
	w := newWorld(t, DefaultLimits(100, 1000))
	w.m.errs = []error{nil, &mail.SendError{Status: 500, Temporary: true}}
	w.register("a@example.test", "en")
	w.work()
	w.ask(mail.KindPasswordReset, "a@example.test", "en")
	w.work()
	w.ask(mail.KindPasswordReset, "a@example.test", "en")
	w.work()
	w.at = w.at.Add(20 * time.Minute)
	w.work()
	if len(w.m.sent) != 3 || !strings.Contains(w.states(), "superseded:1") || !strings.Contains(w.states(), "sent:2") {
		t.Fatalf("%d sends, %s", len(w.m.sent), w.states())
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
