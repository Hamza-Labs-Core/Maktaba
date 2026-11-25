// Package outbox keeps the account mails until they are sent (table mail_outbox, migration
// 00580002) and sends them, so a mail survives a restart and no request waits for, fails with,
// or tells anything by, the mail it causes.
//
// A request only writes one row (Request, Register, Send): the same one whether or not the
// address has an account. What the row becomes is decided later, by the api role's Run:
//
//	resolve  a request → the account is looked up, its link made, the mail written (or the row
//	         dropped: no account, or too many to this address)
//	send     a written mail → the mailer, within the day's and the month's limits, with the
//	         row's id as the idempotency key; tried again later, less and less often, until it
//	         is too old to be of use
//
// Several api nodes may Run at once: each step takes its row under one lock. The worker role
// only writes rows. A row's contents are sealed, its address kept as a keyed hash, and both
// the contents and, after Retention, the row go.
package outbox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// The classes of mail, by whom it goes to. A request's class is decided when it is worked on,
// never when it is written.
//
//	signup   an address nobody has proved is theirs (a confirmation, a reset of an account never
//	         confirmed), and what a stranger's sign-up alone causes (already registered)
//	repeat   a confirmed account's reset, past the day's first resetsFirst
//	account  a confirmed account's reset
//	notice   an account's notice
//
// signup and repeat may each use only a share of the day's and the month's limits: the rest is
// kept for the mails of confirmed accounts, whatever strangers ask for.
const (
	ClassSignup  = "signup"
	ClassRepeat  = "repeat"
	ClassAccount = "account"
	ClassNotice  = "notice"
)

// resetsFirst is how many resets a day a confirmed account gets before they count as repeats.
const resetsFirst = 3

// The states of a row.
const (
	StateQueued     = "queued"
	StateReady      = "ready"
	StateSending    = "sending"
	StateSent       = "sent"
	StateLogged     = "logged" // the log driver: not delivered, and not counted
	StateFailed     = "failed"
	StateExpired    = "expired"
	StateSuppressed = "suppressed"
	StateSuperseded = "superseded"
)

// Retention is how long a finished row stays, for the month's limit.
const Retention = 35 * 24 * time.Hour

// MaxQueued is how many requests may wait to be worked on; past it new ones are dropped, so a
// flood can't fill the database. Working on one takes milliseconds.
const MaxQueued = 5000

// advisory lock of resolve and claim: the limits are counted and taken one at a time
const lockID = 8472620

// How long each kind waits before it is too old to be of use, and the link's own life from when
// the mail is written. A link is always good for at least half its life once the mail arrives.
const (
	waitRequest = 23 * time.Hour // a request, before its mail is written
	waitReset   = time.Hour
	waitNotice  = 23 * time.Hour
	// no mail is tried again past 23 hours: the provider forgets an idempotency key after 24
)

// Rate is how many mails of one kind one address gets: Burst in an hour, then one each Gap, and
// Day in a day (0: no limit by day). So a mail is refused for its rate only when one of its kind
// was made less than Gap ago (or, with Day, less than a day ago), whose link is the newest one.
type Rate struct {
	Burst int
	Gap   time.Duration
	Day   int
}

// kindResetUnproved is a reset's rate for an account whose address nobody has confirmed.
const kindResetUnproved = mail.KindPasswordReset + "/unproved"

// Limits are the outbox's limits. A zero is no limit.
type Limits struct {
	Day, Month int
	// SignupShare and RepeatShare are the parts of Day and Month the signup class, and it and the
	// repeat class together, may use, in percent.
	SignupShare, RepeatShare int
	Address                  map[string]Rate // by kind; "" is any other kind
}

// DefaultLimits are a sending deployment's, with the day's and the month's given.
func DefaultLimits(day, month int) Limits {
	return Limits{Day: day, Month: month, SignupShare: 60, RepeatShare: 80, Address: map[string]Rate{
		// its link is good for a day: the sixth's refusal leaves one of that day's
		mail.KindVerifyEmail: {Burst: 3, Gap: 15 * time.Minute, Day: 6},
		// its link is good for an hour. No limit by day: whoever asked before, the newest link in
		// the inbox has 45 minutes left, or a fresh one is sent
		mail.KindPasswordReset: {Burst: 3, Gap: 15 * time.Minute},
		kindResetUnproved:      {Burst: 2, Gap: time.Hour},
		// asked for by whoever types the address, never by its owner's own click
		mail.KindAlreadyRegistered: {Burst: 1, Gap: time.Hour, Day: 3},
		"":                         {Burst: 20, Gap: time.Hour, Day: 40},
	}}
}

// Outbox writes and sends the mails.
type Outbox struct {
	DB        *sql.DB
	Mailer    mail.Mailer // delivers; only Run and its steps use it
	Templates mail.Templates
	Limits    Limits
	// Logs: the mailer only writes to the log (what it takes is logged, not sent)
	Logs   bool
	Logger *slog.Logger
	// Senders is how many mails are being handed to the mailer at once by this process
	// (default 2); SendTimeout bounds one (default 45 s).
	Senders     int
	SendTimeout time.Duration
	Poll        time.Duration // default 3 s: what another process wrote, and what waited

	tokens *stores.MailTokens
	users  *stores.Users
	box    *staffauth.Box
	hash   []byte
	wake   chan struct{}
	now    func() time.Time

	mu      sync.Mutex
	lastLog map[string]time.Time
}

// New wires an outbox; secret is the token secret its sealing and hashing keys derive from.
func New(db *sql.DB, secret string, m mail.Mailer, t mail.Templates, l Limits, logger *slog.Logger) (*Outbox, error) {
	if len(secret) < 32 {
		return nil, errors.New("mail outbox: the token secret is not set")
	}
	key := sha256.Sum256([]byte("maktaba mail outbox box\x00" + secret))
	box, err := staffauth.NewBox(key[:])
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte("maktaba mail address hash\x00" + secret))
	return &Outbox{DB: db, Mailer: m, Templates: t, Limits: l, Logger: logger, Senders: 2, SendTimeout: 45 * time.Second,
		Poll: 3 * time.Second, tokens: stores.NewMailTokens(db), users: stores.NewUsers(db), box: box, hash: h[:],
		wake: make(chan struct{}, 8), now: time.Now, lastLog: map[string]time.Time{}}, nil
}

// request is what a queued row holds.
type request struct {
	To string `json:"to"`
}

func (o *Outbox) toHash(addr string) string {
	m := hmac.New(sha256.New, o.hash)
	_, _ = m.Write([]byte(strings.ToLower(strings.TrimSpace(addr))))
	return hex.EncodeToString(m.Sum(nil))
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type rower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func classOf(kind string) string {
	switch kind {
	case mail.KindVerifyEmail, mail.KindAlreadyRegistered:
		return ClassSignup
	case mail.KindPasswordReset:
		return ClassAccount
	}
	return ClassNotice
}

// put writes one row. A queued one past MaxQueued is dropped.
func (o *Outbox) put(ctx context.Context, q execer, id, kind, to, lang, state string, payload any, wait time.Duration) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sealed, err := o.box.Seal(string(raw), id)
	if err != nil {
		return err
	}
	res, err := q.ExecContext(ctx, `
        INSERT INTO mail_outbox (id, kind, class, to_hash, lang, state, sealed, created_at, next_at, expires_at)
        SELECT $1::uuid, $2::text, $3::text, $4::text, $5::text, $6::text, $7::text, $8::timestamptz, $8::timestamptz, $9::timestamptz
         WHERE $6::text <> 'queued'
            OR (SELECT count(*) FROM (SELECT 1 FROM mail_outbox WHERE state = 'queued' LIMIT $10::int) x) < $10::int`,
		id, kind, classOf(kind), o.toHash(to), lang, state, sealed, o.now().UTC(), o.now().UTC().Add(wait), MaxQueued)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		o.seldom("full", "mail: too many requests are waiting; one was dropped", "event", "mail_queue_full")
	}
	return nil
}

// Request asks for a verification link or a password reset for addr, in lang ("" is the
// account's). It does the same whether or not addr has an account.
func (o *Outbox) Request(ctx context.Context, kind, addr, lang string) error {
	wait := waitRequest
	if kind == mail.KindPasswordReset {
		wait = waitReset
	}
	if err := o.put(ctx, o.DB, uuid.NewString(), kind, addr, lang, StateQueued, request{To: addr}, wait); err != nil {
		return err
	}
	o.Wake()
	return nil
}

// Register creates an unverified password account and asks for its verification mail, or for
// the notice to the account the address already has: the same two statements and one commit
// either way.
func (o *Outbox) Register(ctx context.Context, email, passwordHash, displayName, lang string) error {
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	created, err := o.users.RegisterTx(ctx, tx, email, passwordHash, displayName, mail.Pick(lang))
	if err != nil {
		return err
	}
	kind := mail.KindVerifyEmail
	if !created {
		kind, lang = mail.KindAlreadyRegistered, ""
	}
	if err := o.put(ctx, tx, uuid.NewString(), kind, email, lang, StateQueued, request{To: email}, waitRequest); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	o.Wake()
	return nil
}

// ErrLimited: the address has had its share of mails of that kind.
var ErrLimited = errors.New("mail: too many to one address")

// Send keeps a written mail (a notice) for sending: the outbox is the roles' mail.Mailer. One
// past its address's share is dropped, with a warning.
func (o *Outbox) Send(ctx context.Context, m mail.Message) error {
	_, err := o.Keep(ctx, m)
	if errors.Is(err, ErrLimited) {
		o.Logger.Warn("mail: too many to one address; not sent", "kind", m.Kind, "event", "mail_address_limit")
		return nil
	}
	return err
}

// Keep is Send, with the row's id, and ErrLimited.
func (o *Outbox) Keep(ctx context.Context, m mail.Message) (string, error) {
	over, err := o.tooMany(ctx, o.DB, o.toHash(m.To), m.Kind, m.Kind)
	if err != nil {
		return "", err
	}
	if over {
		return "", ErrLimited
	}
	m.ID = uuid.NewString()
	if err := o.put(ctx, o.DB, m.ID, m.Kind, m.To, m.Lang, StateReady, m, waitNotice); err != nil {
		return "", err
	}
	o.Wake()
	return m.ID, nil
}

// Wake makes Run look now.
func (o *Outbox) Wake() {
	for range cap(o.wake) {
		select {
		case o.wake <- struct{}{}:
		default:
			return
		}
	}
}

// Run works the outbox until ctx ends.
func (o *Outbox) Run(ctx context.Context) {
	var wg sync.WaitGroup
	loop := func(step func(context.Context) (bool, error)) {
		defer wg.Done()
		t := time.NewTicker(o.Poll)
		defer t.Stop()
		for {
			for ctx.Err() == nil {
				more, err := step(ctx)
				if err != nil && ctx.Err() == nil {
					o.seldom("step", "mail: the outbox could not be worked", "err", err, "event", "mail_outbox_error")
				}
				if err != nil || !more {
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-o.wake:
			}
		}
	}
	wg.Add(2 + o.Senders)
	go loop(o.Resolve)
	for range o.Senders {
		go loop(o.SendOne)
	}
	go func() {
		defer wg.Done()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if err := o.Sweep(ctx); err != nil && ctx.Err() == nil {
				o.seldom("sweep", "mail: the outbox could not be swept", "err", err, "event", "mail_outbox_error")
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	wg.Wait()
}

// Sweep deletes the rows finished longer than Retention ago, and closes the ones past their time.
func (o *Outbox) Sweep(ctx context.Context) error {
	now := o.now().UTC()
	if _, err := o.DB.ExecContext(ctx, `
        UPDATE mail_outbox SET state = 'expired', sealed = NULL, finished_at = $1
         WHERE expires_at <= $1 AND (state IN ('queued', 'ready') OR (state = 'sending' AND lease_until < $1))`, now); err != nil {
		return err
	}
	_, err := o.DB.ExecContext(ctx, `DELETE FROM mail_outbox WHERE finished_at < $1`, now.Add(-Retention))
	return err
}

// locked runs f in a transaction holding the outbox's lock.
func (o *Outbox) locked(ctx context.Context, f func(tx *sql.Tx) error) error {
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
		return err
	}
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// tooMany reports whether the address behind hash has had its share of kind's mails, at rate's
// name. Only mails written count: a request refused for its rate is dropped and leaves nothing.
func (o *Outbox) tooMany(ctx context.Context, q rower, hash, kind, rateOf string) (bool, error) {
	rate, ok := o.Limits.Address[rateOf]
	if !ok {
		rate = o.Limits.Address[""]
	}
	if rate.Burst == 0 {
		return false, nil
	}
	now := o.now().UTC()
	var hour, day int
	var last sql.NullTime
	if err := q.QueryRowContext(ctx, `
        SELECT count(*) FILTER (WHERE created_at > $3), count(*), max(created_at)
          FROM mail_outbox WHERE to_hash = $1 AND kind = $2 AND created_at > $4 AND state IN ('ready', 'sending', 'sent', 'logged', 'suppressed', 'superseded')`,
		hash, kind, now.Add(-time.Hour), now.Add(-24*time.Hour)).Scan(&hour, &day, &last); err != nil {
		return false, err
	}
	soon := hour >= rate.Burst && last.Valid && now.Sub(last.Time) < rate.Gap
	return soon || (rate.Day > 0 && day >= rate.Day), nil
}

func finish(ctx context.Context, q execer, id, state, why string, now time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE mail_outbox SET state = $2, sealed = NULL, finished_at = $3, lease_until = NULL, last_error = $4 WHERE id = $1::uuid`,
		id, state, now, why)
	return err
}

// Resolve works one request, oldest first, and reports whether there was one.
func (o *Outbox) Resolve(ctx context.Context) (bool, error) {
	found, wrote := false, false
	err := o.locked(ctx, func(tx *sql.Tx) error {
		now := o.now().UTC()
		var id, kind, hash, lang, sealed string
		err := tx.QueryRowContext(ctx, `
            SELECT id::text, kind, to_hash, lang, sealed FROM mail_outbox
             WHERE state = 'queued' AND expires_at > $1 ORDER BY created_at, id LIMIT 1 FOR UPDATE`, now).
			Scan(&id, &kind, &hash, &lang, &sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		var req request
		if plain, err := o.box.Open(sealed, id); err != nil || json.Unmarshal([]byte(plain), &req) != nil {
			o.Logger.Error("mail: a request does not open (was the token secret changed?)", "mail", id, "kind", kind, "event", "mail_failed")
			return finish(ctx, tx, id, StateFailed, "unreadable", now)
		}
		drop := func() error {
			_, err := tx.ExecContext(ctx, `DELETE FROM mail_outbox WHERE id = $1::uuid`, id)
			return err
		}
		u, ok, err := o.wants(ctx, kind, req.To)
		if err != nil {
			return err
		}
		if !ok {
			return drop()
		}
		class, rateOf := ClassSignup, kind
		if kind == mail.KindPasswordReset {
			if !u.EmailVerified {
				rateOf = kindResetUnproved
			} else if class, err = o.resetClass(ctx, tx, hash); err != nil {
				return err
			}
		}
		// before the link is made: the one sent last stays good
		if over, err := o.tooMany(ctx, tx, hash, kind, rateOf); err != nil {
			return err
		} else if over {
			o.seldom("address", "mail: too many to one address; not sent", "mail", id, "kind", kind, "event", "mail_address_limit")
			return drop()
		}
		lang = mail.Pick(lang, u.Locale)
		m, wait, err := o.write(ctx, tx, kind, lang, u)
		if err != nil {
			return err
		}
		m.ID = id
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if sealed, err = o.box.Seal(string(raw), id); err != nil {
			return err
		}
		// its link is the only good one now: one written before and not yet sent is not sent
		if _, err := tx.ExecContext(ctx, `UPDATE mail_outbox SET state = 'superseded', sealed = NULL, finished_at = $3
             WHERE to_hash = $1 AND kind = $2 AND state = 'ready'`, hash, kind, now); err != nil {
			return err
		}
		wrote = true
		_, err = tx.ExecContext(ctx, `UPDATE mail_outbox SET state = 'ready', class = $6, written = nextval(pg_get_serial_sequence('mail_outbox', 'written')), sealed = $2, lang = $3, next_at = $4, expires_at = $5 WHERE id = $1::uuid`,
			id, sealed, lang, now, now.Add(wait), class)
		return err
	})
	if err == nil && wrote {
		o.Wake()
	}
	return found, err
}

// wants is the account behind addr, and whether it still wants a mail of kind: there is one, it
// is not suspended, and (a confirmation) it is not confirmed yet.
func (o *Outbox) wants(ctx context.Context, kind, addr string) (stores.User, bool, error) {
	u, err := o.users.ByEmail(ctx, addr)
	if errors.Is(err, stores.ErrUserNotFound) {
		return u, false, nil
	}
	if err != nil {
		return u, false, err
	}
	if u.Status != stores.StatusActive || (kind == mail.KindVerifyEmail && u.EmailVerified) {
		return u, false, nil
	}
	return u, true, nil
}

// resetClass is a confirmed account's reset's class: account for the day's first few, then repeat.
func (o *Outbox) resetClass(ctx context.Context, tx *sql.Tx, hash string) (string, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mail_outbox WHERE to_hash = $1 AND kind = $2 AND created_at > $3 AND state NOT IN ('queued', 'failed', 'expired')`,
		hash, mail.KindPasswordReset, o.now().UTC().Add(-24*time.Hour)).Scan(&n)
	if n >= resetsFirst {
		return ClassRepeat, err
	}
	return ClassAccount, err
}

// asked reports whether kind is one a person asks for (its mail is about the account's state and
// may go stale while it waits).
func asked(kind string) bool {
	return kind == mail.KindVerifyEmail || kind == mail.KindPasswordReset || kind == mail.KindAlreadyRegistered
}

// write makes the mail of a request, and how long it may wait to be sent.
func (o *Outbox) write(ctx context.Context, tx *sql.Tx, kind, lang string, u stores.User) (mail.Message, time.Duration, error) {
	switch kind {
	case mail.KindVerifyEmail:
		tok, err := o.tokens.IssueTx(ctx, tx, u.ID, stores.PurposeVerifyEmail, stores.VerifyEmailTTL)
		return o.Templates.VerifyEmail(lang, u.Email, tok), stores.VerifyEmailTTL / 2, err
	case mail.KindPasswordReset:
		tok, err := o.tokens.IssueTx(ctx, tx, u.ID, stores.PurposePasswordReset, stores.PasswordResetTTL)
		return o.Templates.PasswordReset(lang, u.Email, tok), stores.PasswordResetTTL / 2, err
	case mail.KindAlreadyRegistered:
		return o.Templates.AlreadyRegistered(lang, u.Email), waitRequest, nil
	}
	return mail.Message{}, 0, fmt.Errorf("mail: a request of an unknown kind %q", kind)
}

// claimed is a row being sent.
type claimed struct {
	id, kind, lang, sealed string
	attempts               int
	stale                  bool // a newer one took its place: closed, not to be sent
}

// claim takes the next mail within the limits, or nil.
func (o *Outbox) claim(ctx context.Context) (*claimed, error) {
	var c *claimed
	err := o.locked(ctx, func(tx *sql.Tx) error {
		now := o.now().UTC()
		var day, month, signupDay, signupMonth, strangeDay, strangeMonth int
		if err := tx.QueryRowContext(ctx, `
            SELECT count(*) FILTER (WHERE at > $2), count(*),
                   count(*) FILTER (WHERE at > $2 AND class = 'signup'), count(*) FILTER (WHERE class = 'signup'),
                   count(*) FILTER (WHERE at > $2 AND class IN ('signup', 'repeat')), count(*) FILTER (WHERE class IN ('signup', 'repeat'))
              FROM (SELECT class, COALESCE(finished_at, $1) AS at FROM mail_outbox
                     WHERE (state = 'sent' AND finished_at > $3) OR (state = 'sending' AND lease_until >= $1)) s`,
			now, now.Add(-24*time.Hour), now.Add(-30*24*time.Hour)).Scan(&day, &month, &signupDay, &signupMonth, &strangeDay, &strangeMonth); err != nil {
			return err
		}
		over := func(n, limit int) bool { return limit > 0 && n >= limit }
		if over(day, o.Limits.Day) || over(month, o.Limits.Month) {
			return o.waits(ctx, tx, now, "", day, month)
		}
		share := func(limit, percent int) int {
			if percent <= 0 || limit <= 0 {
				return 0
			}
			return max(1, limit*percent/100)
		}
		past := func(d, m, percent int) bool {
			return over(d, share(o.Limits.Day, percent)) || over(m, share(o.Limits.Month, percent))
		}
		noRepeat := past(strangeDay, strangeMonth, o.Limits.RepeatShare)
		noSignup := noRepeat || past(signupDay, signupMonth, o.Limits.SignupShare)
		var k claimed
		var hash string
		err := tx.QueryRowContext(ctx, `
            SELECT id::text, kind, lang, sealed, attempts, to_hash FROM mail_outbox
             WHERE ((state = 'ready' AND next_at <= $1) OR (state = 'sending' AND lease_until < $1))
               AND expires_at > $1 AND NOT ($2::boolean AND class = 'signup') AND NOT ($3::boolean AND class = 'repeat')
             ORDER BY CASE class WHEN 'signup' THEN 2 WHEN 'repeat' THEN 1 ELSE 0 END, next_at, id LIMIT 1 FOR UPDATE`, now, noSignup, noRepeat).
			Scan(&k.id, &k.kind, &k.lang, &k.sealed, &k.attempts, &hash)
		if errors.Is(err, sql.ErrNoRows) {
			if noSignup {
				return o.waits(ctx, tx, now, ClassSignup, day, month)
			}
			return nil
		}
		if err != nil {
			return err
		}
		// a newer one of its kind made its link dead while it was being sent or waited
		if asked(k.kind) {
			if err := tx.QueryRowContext(ctx, `
                SELECT EXISTS (SELECT 1 FROM mail_outbox n JOIN mail_outbox m ON m.id = $1::uuid
                  WHERE n.to_hash = $2 AND n.kind = $3 AND n.state <> 'queued' AND n.written > m.written)`,
				k.id, hash, k.kind).Scan(&k.stale); err != nil {
				return err
			}
			if k.stale {
				c = &k
				return finish(ctx, tx, k.id, StateSuperseded, "", now)
			}
		}
		k.attempts++
		// the send, and time to say so
		lease := now.Add(o.SendTimeout + 30*time.Second)
		if _, err := tx.ExecContext(ctx, `UPDATE mail_outbox SET state = 'sending', lease_until = $2, attempts = $3 WHERE id = $1::uuid`,
			k.id, lease, k.attempts); err != nil {
			return err
		}
		c = &k
		return nil
	})
	return c, err
}

// waits says, now and then, that mails wait for a limit.
func (o *Outbox) waits(ctx context.Context, tx *sql.Tx, now time.Time, class string, day, month int) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mail_outbox WHERE state = 'ready' AND expires_at > $1 AND ($2::text = '' OR class = $2::text)`,
		now, class).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		o.seldom("limit"+class, "mail: the sending limit is reached; mails wait", "waiting", n, "class", class, "sent_24h", day,
			"sent_30d", month, "event", "mail_limit_reached")
	}
	return nil
}

// Backoff is how long a mail waits after its nth failed attempt.
func Backoff(attempts int) time.Duration {
	steps := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	if attempts-1 < len(steps) {
		return steps[max(0, attempts-1)]
	}
	return time.Hour
}

// SendOne sends one mail and reports whether there was one.
func (o *Outbox) SendOne(ctx context.Context) (bool, error) {
	c, err := o.claim(ctx)
	if err != nil || c == nil {
		return false, err
	}
	if c.stale {
		return true, nil
	}
	// what happened to a claimed mail is kept even while the process stops
	keepCtx, cancelKeep := context.WithTimeout(context.WithoutCancel(ctx), o.SendTimeout+15*time.Second)
	defer cancelKeep()
	log := o.Logger.With("mail", c.id, "kind", c.kind, "lang", c.lang, "attempt", c.attempts)

	var m mail.Message
	if plain, err := o.box.Open(c.sealed, c.id); err != nil || json.Unmarshal([]byte(plain), &m) != nil {
		log.Error("mail: a mail does not open (was the token secret changed?)", "event", "mail_failed")
		return true, finish(keepCtx, o.DB, c.id, StateFailed, "unreadable", o.now().UTC())
	}
	// the account may have been closed or suspended, or confirmed, while its mail waited
	if asked(c.kind) {
		_, ok, err := o.wants(ctx, c.kind, m.To)
		if err != nil {
			return false, errors.Join(err, o.again(keepCtx, c.id, o.now().UTC(), ""))
		}
		if !ok {
			return true, finish(keepCtx, o.DB, c.id, StateSuperseded, "", o.now().UTC())
		}
	}
	sendCtx, cancel := context.WithTimeout(ctx, o.SendTimeout)
	err = o.Mailer.Send(sendCtx, m)
	cancel()
	now := o.now().UTC()
	var se *mail.SendError
	switch {
	case err == nil:
		if o.Logs {
			return true, finish(keepCtx, o.DB, c.id, StateLogged, "", now)
		}
		log.Info("mail: sent", "event", "mail_sent")
		return true, finish(keepCtx, o.DB, c.id, StateSent, "", now)
	case errors.Is(err, mail.ErrSuppressed):
		log.Info("mail: not sent, this deployment does not mail the recipient", "event", "mail_suppressed")
		return true, finish(keepCtx, o.DB, c.id, StateSuppressed, "", now)
	case ctx.Err() != nil:
		// stopping: the next process sends it, with the same idempotency key
		return true, o.again(keepCtx, c.id, now, "stopped")
	case errors.As(err, &se) && !se.Temporary:
		log.Error("mail: the provider did not take it", "err", err, "event", "mail_failed")
		return true, finish(keepCtx, o.DB, c.id, StateFailed, err.Error(), now)
	}
	wait := Backoff(c.attempts)
	if se != nil {
		switch {
		case se.Quota:
			wait = max(wait, time.Hour)
		case se.Refused:
			wait = max(wait, 5*time.Minute)
		}
		wait = max(wait, se.RetryAfter)
	}
	if se != nil && (se.Quota || se.Refused) {
		log.Error("mail: the provider refuses to send", "err", err, "again_in", wait.String(), "event", "mail_provider_refused")
	} else {
		log.Warn("mail: not sent, to be tried again", "err", err, "again_in", wait.String(), "event", "mail_retry")
	}
	return true, o.again(keepCtx, c.id, now.Add(wait), errText(err))
}

// errText is an error for the row: a provider's answer, or only that there was none (an error
// of the network may name more than it should).
func errText(err error) string {
	var se *mail.SendError
	if errors.As(err, &se) && se.Status != 0 {
		return se.Error()
	}
	return "no answer"
}

func (o *Outbox) again(ctx context.Context, id string, at time.Time, why string) error {
	_, err := o.DB.ExecContext(ctx, `UPDATE mail_outbox SET state = 'ready', next_at = $2, lease_until = NULL, last_error = $3 WHERE id = $1::uuid AND state = 'sending'`,
		id, at, why)
	return err
}

// Forget takes an address out of the outbox, for an account being erased: its mails waiting are
// not sent, and the ones sent stay in the counts only, with no address.
func (o *Outbox) Forget(ctx context.Context, addr string) error {
	hash := o.toHash(addr)
	if _, err := o.DB.ExecContext(ctx, `DELETE FROM mail_outbox WHERE to_hash = $1 AND state <> 'sent'`, hash); err != nil {
		return err
	}
	_, err := o.DB.ExecContext(ctx, `UPDATE mail_outbox SET to_hash = '' WHERE to_hash = $1`, hash)
	return err
}

// seldom warns at most once a minute for each key.
func (o *Outbox) seldom(key, msg string, args ...any) {
	o.mu.Lock()
	last := o.lastLog[key]
	now := time.Now()
	if now.Sub(last) < time.Minute {
		o.mu.Unlock()
		return
	}
	o.lastLog[key] = now
	o.mu.Unlock()
	o.Logger.Warn(msg, args...)
}

// Counts is how many mails of the last 30 days are in each state, and how many were sent in
// the last day.
type Counts struct {
	States  map[string]int `json:"states"`
	Sent24h int            `json:"sent_24h"`
	Sent30d int            `json:"sent_30d"`
	// LastErrors are the refusals of the mails still waiting or given up in the last day.
	LastErrors map[string]int `json:"last_errors,omitempty"`
}

// Count reads them (`maktaba-cloud mail status`).
func Count(ctx context.Context, db *sql.DB) (Counts, error) {
	c := Counts{States: map[string]int{}, LastErrors: map[string]int{}}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE finished_at > now() - INTERVAL '1 day'), count(*)
        FROM mail_outbox WHERE state = 'sent' AND finished_at > now() - INTERVAL '30 days'`).Scan(&c.Sent24h, &c.Sent30d); err != nil {
		return c, err
	}
	into := func(m map[string]int, q string) error {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			m[k] = n
		}
		return rows.Err()
	}
	if err := into(c.States, `SELECT state, count(*) FROM mail_outbox WHERE created_at > now() - INTERVAL '30 days' GROUP BY state`); err != nil {
		return c, err
	}
	return c, into(c.LastErrors, `SELECT last_error, count(*) FROM mail_outbox WHERE last_error <> '' AND created_at > now() - INTERVAL '1 day' AND state <> 'sent' GROUP BY last_error`)
}

// State is the state of one mail and its last refusal.
func State(ctx context.Context, db *sql.DB, id string) (state, lastError string, err error) {
	err = db.QueryRowContext(ctx, `SELECT state, last_error FROM mail_outbox WHERE id = $1::uuid`, id).Scan(&state, &lastError)
	return state, lastError, err
}
