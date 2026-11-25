// Package outbox keeps the account mails until they are sent (table mail_outbox, migration
// 00580002) and sends them, so a mail survives a restart and no request waits for, fails with,
// or tells anything by, the mail it causes.
//
// A request only writes one row (Request, Register, Send): the same one whether or not the
// address has an account. What the row becomes is decided later, by the api role's Run:
//
//	resolve  a request → is there an account that wants it, whose mail is it, is one of its kind
//	         to this address too recent? A row nothing comes of is dropped (kept a day, without
//	         its address, for its network's count)
//	send     within the day's and the month's limits: only now is the link made and the mail
//	         written, and handed to the mailer, with the row's id as the idempotency key; tried
//	         again later, less and less often, until it is too old to be of use
//
// A link is never ended by a later request or mail: every one sent is good for its own life, until
// one of them is used (stores.MailTokens.Redeem).
//
// Several api nodes may Run at once: each step takes its row under one lock. The worker role
// only writes rows. A row's contents are sealed, its address and its requester's network kept as
// keyed hashes; the contents go once the mail is sent, the network after a day, the row after
// Retention.
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

// The classes of mail, by whom it goes to.
//
//	stranger an address nobody has proved is theirs (a confirmation, a reset of an account never
//	         confirmed), and what a stranger's sign-up alone causes (already registered)
//	repeat   a confirmed account's reset, when one was sent to it in the last hour
//	account  a confirmed account's reset, the first of the hour
//	notice   an account's notice
const (
	ClassStranger = "stranger"
	ClassRepeat   = "repeat"
	ClassAccount  = "account"
	ClassNotice   = "notice"
)

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
	StateDropped    = "dropped" // no account wants it, or one was sent a moment ago
)

// Retention is how long a finished row stays, for the month's limit.
const Retention = 35 * 24 * time.Hour

// MaxQueued is how many requests may wait to be worked on; past it new ones are dropped, so a
// flood can't fill the database. Working on one takes milliseconds.
const MaxQueued = 5000

// advisory lock of resolve and claim: the limits are counted and taken one at a time
const lockID = 8472620

// How long a mail may wait to be sent: a reset is of use for an hour; no mail is tried past 23
// hours, as the provider forgets an idempotency key after 24. Once its link is made it is sent
// within half the link's life, or not.
const (
	waitReset = time.Hour
	waitOther = 23 * time.Hour
)

// Rate is how often one address gets a mail of one kind: one each Gap, and Day in 24 hours (0: no
// limit by day).
type Rate struct {
	Gap time.Duration
	Day int
}

// Window is how many asks one network makes in any hour, and in any day.
type Window struct{ Hour, Day int }

// kindResetUnproved is a reset's rate for an account whose address nobody has confirmed.
const kindResetUnproved = mail.KindPasswordReset + "/unproved"

// Limits are the outbox's limits. A zero is no limit.
type Limits struct {
	Day, Month int
	// What of Day and Month, in percent, strangers' mails asked for without the bot check may use
	// (Unchecked), strangers' mails in all (Strangers), and they and the repeats (Repeats). The
	// rest is kept for confirmed accounts: of 85 a day, 26, a reset an hour for one of them.
	Unchecked, Strangers, Repeats int
	// One network's asks, without the bot check and with it; each counts its own.
	Net, NetChecked Window
	Address         map[string]Rate // by kind
}

// DefaultLimits are a sending deployment's, with the day's and the month's given.
func DefaultLimits(day, month int) Limits {
	return Limits{Day: day, Month: month, Unchecked: 40, Strangers: 60, Repeats: 70,
		Net: Window{Hour: 10, Day: 20}, NetChecked: Window{Hour: 40, Day: 60},
		Address: map[string]Rate{
			mail.KindVerifyEmail:   {Gap: 15 * time.Minute, Day: 6},
			mail.KindPasswordReset: {Gap: 15 * time.Minute},
			kindResetUnproved:      {Gap: time.Hour},
			// asked for by whoever types the address, never by its owner's own click
			mail.KindAlreadyRegistered: {Gap: 8 * time.Hour},
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

// item is what a row holds, sealed: whom to, and the mail once it is written.
type item struct {
	To  string        `json:"to"`
	Msg *mail.Message `json:"msg,omitempty"`
}

func (o *Outbox) keyed(v string) string {
	m := hmac.New(sha256.New, o.hash)
	_, _ = m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}

func (o *Outbox) toHash(addr string) string {
	return o.keyed(strings.ToLower(strings.TrimSpace(addr)))
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (o *Outbox) seal(id string, it item) (string, error) {
	raw, err := json.Marshal(it)
	if err != nil {
		return "", err
	}
	return o.box.Seal(string(raw), id)
}

func (o *Outbox) open(id, sealed string) (item, bool) {
	var it item
	plain, err := o.box.Open(sealed, id)
	return it, err == nil && json.Unmarshal([]byte(plain), &it) == nil
}

// TooMany refuses an ask: its network has made its share. It says nothing of the address.
type TooMany struct{ RetryAfter time.Duration }

func (t *TooMany) Error() string { return "mail: too many asks from one network" }

// ask writes an ask's row in tx, or refuses it (*TooMany) for its network, whose asks are counted
// one at a time, whatever came of them.
func (o *Outbox) ask(ctx context.Context, tx *sql.Tx, a mail.Ask) error {
	now := o.now().UTC()
	net := ""
	if a.Net != "" {
		net = o.keyed("net " + a.Net)
	}
	w := o.Limits.Net
	if a.Checked {
		w = o.Limits.NetChecked
	}
	if net != "" && w.Day > 0 {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, net); err != nil {
			return err
		}
		var hour, day int
		var oldHour, oldDay sql.NullTime
		if err := tx.QueryRowContext(ctx, `
            SELECT count(*) FILTER (WHERE created_at > $3), count(*), min(created_at) FILTER (WHERE created_at > $3), min(created_at)
              FROM mail_outbox WHERE net_hash = $1 AND checked = $2 AND created_at > $4`,
			net, a.Checked, now.Add(-time.Hour), now.Add(-24*time.Hour)).Scan(&hour, &day, &oldHour, &oldDay); err != nil {
			return err
		}
		switch {
		case day >= w.Day:
			return &TooMany{RetryAfter: oldDay.Time.Add(24 * time.Hour).Sub(now)}
		case hour >= w.Hour:
			return &TooMany{RetryAfter: oldHour.Time.Add(time.Hour).Sub(now)}
		}
	}
	wait := waitOther
	if a.Kind == mail.KindPasswordReset {
		wait = waitReset
	}
	id := uuid.NewString()
	sealed, err := o.seal(id, item{To: a.To})
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `
        INSERT INTO mail_outbox (id, kind, to_hash, net_hash, checked, lang, state, sealed, created_at, next_at, expires_at)
        SELECT $1::uuid, $2::text, $3::text, $4::text, $5::boolean, $6::text, 'queued', $7::text, $8::timestamptz, $8::timestamptz, $9::timestamptz
         WHERE (SELECT count(*) FROM (SELECT 1 FROM mail_outbox WHERE state = 'queued' LIMIT $10::int) x) < $10::int`,
		id, a.Kind, o.toHash(a.To), net, a.Checked, a.Lang, sealed, now, now.Add(wait), MaxQueued)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		o.seldom("full", "mail: too many requests are waiting; one was dropped", "event", "mail_queue_full")
	}
	return nil
}

// Request asks for a verification link or a password reset (a.Lang "" is the account's). It does
// the same whether or not a.To has an account.
func (o *Outbox) Request(ctx context.Context, a mail.Ask) error {
	return o.asking(ctx, func(tx *sql.Tx) error { return o.ask(ctx, tx, a) })
}

// Register creates an unverified password account and asks for its verification mail, or for
// the notice to the account the address already has: the same statements and one commit either
// way.
func (o *Outbox) Register(ctx context.Context, a mail.Ask, passwordHash, displayName string) error {
	return o.asking(ctx, func(tx *sql.Tx) error {
		created, err := o.users.RegisterTx(ctx, tx, a.To, passwordHash, displayName, mail.Pick(a.Lang))
		if err != nil {
			return err
		}
		a.Kind = mail.KindVerifyEmail
		if !created {
			a.Kind, a.Lang = mail.KindAlreadyRegistered, ""
		}
		return o.ask(ctx, tx, a)
	})
}

func (o *Outbox) asking(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := f(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	o.Wake()
	return nil
}

// Send keeps a written mail (a notice) for sending: the outbox is the roles' mail.Mailer.
func (o *Outbox) Send(ctx context.Context, m mail.Message) error {
	_, err := o.Keep(ctx, m)
	return err
}

// Keep is Send, with the row's id.
func (o *Outbox) Keep(ctx context.Context, m mail.Message) (string, error) {
	m.ID = uuid.NewString()
	sealed, err := o.seal(m.ID, item{To: m.To, Msg: &m})
	if err != nil {
		return "", err
	}
	now := o.now().UTC()
	if _, err := o.DB.ExecContext(ctx, `
        INSERT INTO mail_outbox (id, kind, class, to_hash, lang, state, sealed, created_at, next_at, expires_at)
        VALUES ($1::uuid, $2, 'notice', $3, $4, 'ready', $5, $6, $6, $7)`,
		m.ID, m.Kind, o.toHash(m.To), m.Lang, sealed, now, now.Add(waitOther)); err != nil {
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

// Sweep deletes the rows finished longer than Retention ago and the dropped ones after a day,
// forgets a day-old row's network, and closes the mails past their time.
func (o *Outbox) Sweep(ctx context.Context) error {
	now := o.now().UTC()
	day := now.Add(-24 * time.Hour)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE mail_outbox SET state = 'expired', sealed = NULL, finished_at = $1
           WHERE expires_at <= $1 AND (state IN ('queued', 'ready') OR (state = 'sending' AND lease_until < $1))`, []any{now}},
		{`DELETE FROM mail_outbox WHERE state = 'dropped' AND created_at <= $1`, []any{day}},
		{`UPDATE mail_outbox SET net_hash = '' WHERE net_hash <> '' AND created_at <= $1`, []any{day}},
		{`DELETE FROM mail_outbox WHERE finished_at < $1`, []any{now.Add(-Retention)}},
	} {
		if _, err := o.DB.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}
	return nil
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

// tooSoon reports whether the address behind hash had a mail of kind less than rate's gap ago, or
// its day's. One still waiting counts (it is sent when there is room); one given up does not.
func (o *Outbox) tooSoon(ctx context.Context, tx *sql.Tx, hash, kind string, rate Rate) (bool, error) {
	if rate.Gap == 0 && rate.Day == 0 {
		return false, nil
	}
	now := o.now().UTC()
	var day int
	var last sql.NullTime
	err := tx.QueryRowContext(ctx, `
        SELECT count(*), max(created_at) FROM mail_outbox
         WHERE to_hash = $1 AND kind = $2 AND created_at > $3 AND state IN ('ready', 'sending', 'sent', 'logged', 'suppressed')`,
		hash, kind, now.Add(-24*time.Hour)).Scan(&day, &last)
	return (last.Valid && now.Sub(last.Time) < rate.Gap) || (rate.Day > 0 && day >= rate.Day), err
}

func finish(ctx context.Context, q execer, id, state, why string, now time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE mail_outbox SET state = $2, sealed = NULL, finished_at = $3, lease_until = NULL, last_error = $4,
        to_hash = CASE WHEN $2 = 'dropped' THEN '' ELSE to_hash END WHERE id = $1::uuid`, id, state, now, why)
	return err
}

// Resolve works one request, oldest first, and reports whether there was one.
func (o *Outbox) Resolve(ctx context.Context) (bool, error) {
	found, ready := false, false
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
		it, ok := o.open(id, sealed)
		if !ok {
			o.Logger.Error("mail: a request does not open (was the token secret changed?)", "mail", id, "kind", kind, "event", "mail_failed")
			return finish(ctx, tx, id, StateFailed, "unreadable", now)
		}
		u, ok, err := o.wants(ctx, kind, it.To)
		if err != nil {
			return err
		}
		if !ok {
			return finish(ctx, tx, id, StateDropped, "", now)
		}
		class, rate := ClassStranger, o.Limits.Address[kind]
		if kind == mail.KindPasswordReset {
			if u.EmailVerified {
				class = ClassAccount
			} else if o.Limits.Address != nil {
				rate = o.Limits.Address[kindResetUnproved]
			}
		}
		if soon, err := o.tooSoon(ctx, tx, hash, kind, rate); err != nil {
			return err
		} else if soon {
			o.seldom("address", "mail: one was sent to this address a moment ago; not sent", "mail", id, "kind", kind, "event", "mail_address_limit")
			return finish(ctx, tx, id, StateDropped, "", now)
		}
		ready = true
		_, err = tx.ExecContext(ctx, `UPDATE mail_outbox SET state = 'ready', class = $2, lang = $3, next_at = $4 WHERE id = $1::uuid`,
			id, class, mail.Pick(lang, u.Locale), now)
		return err
	})
	if err == nil && ready {
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

// write makes the mail of a request, with its link, and how long it may still wait.
func (o *Outbox) write(ctx context.Context, tx *sql.Tx, kind, lang string, u stores.User) (mail.Message, time.Duration, error) {
	switch kind {
	case mail.KindVerifyEmail:
		tok, err := o.tokens.IssueTx(ctx, tx, u.ID, stores.PurposeVerifyEmail, stores.VerifyEmailTTL)
		return o.Templates.VerifyEmail(lang, u.Email, tok), stores.VerifyEmailTTL / 2, err
	case mail.KindPasswordReset:
		tok, err := o.tokens.IssueTx(ctx, tx, u.ID, stores.PurposePasswordReset, stores.PasswordResetTTL)
		return o.Templates.PasswordReset(lang, u.Email, tok), stores.PasswordResetTTL / 2, err
	case mail.KindAlreadyRegistered:
		return o.Templates.AlreadyRegistered(lang, u.Email), waitOther, nil
	}
	return mail.Message{}, 0, fmt.Errorf("mail: a request of an unknown kind %q", kind)
}

// claimed is a row being sent.
type claimed struct {
	id, kind, lang string
	attempts       int
	msg            *mail.Message // nil: closed, there is nothing to send
}

// asked reports whether kind is one a person asks for (its account may be gone by the time it goes).
func asked(kind string) bool {
	return kind == mail.KindVerifyEmail || kind == mail.KindPasswordReset || kind == mail.KindAlreadyRegistered
}

// claim takes the next mail within the limits, its link made and its words written, or nil.
func (o *Outbox) claim(ctx context.Context) (*claimed, error) {
	var c *claimed
	err := o.locked(ctx, func(tx *sql.Tx) error {
		now := o.now().UTC()
		type count struct{ day, month int }
		var all, unchecked, strangers, repeats count
		if err := tx.QueryRowContext(ctx, `
            SELECT count(*) FILTER (WHERE at > $2), count(*),
                   count(*) FILTER (WHERE at > $2 AND class = 'stranger' AND NOT checked), count(*) FILTER (WHERE class = 'stranger' AND NOT checked),
                   count(*) FILTER (WHERE at > $2 AND class = 'stranger'), count(*) FILTER (WHERE class = 'stranger'),
                   count(*) FILTER (WHERE at > $2 AND class IN ('stranger', 'repeat')), count(*) FILTER (WHERE class IN ('stranger', 'repeat'))
              FROM (SELECT class, checked, COALESCE(finished_at, $1) AS at FROM mail_outbox
                     WHERE (state = 'sent' AND finished_at > $3) OR (state = 'sending' AND lease_until >= $1)) s`,
			now, now.Add(-24*time.Hour), now.Add(-30*24*time.Hour)).Scan(&all.day, &all.month, &unchecked.day, &unchecked.month,
			&strangers.day, &strangers.month, &repeats.day, &repeats.month); err != nil {
			return err
		}
		full := func(n count, percent int) bool {
			over := func(n, limit int) bool { return limit > 0 && percent > 0 && n >= max(1, limit*percent/100) }
			return over(n.day, o.Limits.Day) || over(n.month, o.Limits.Month)
		}
		if full(all, 100) {
			return o.waits(ctx, tx, now, "", all.day, all.month)
		}
		noRepeat := full(repeats, o.Limits.Repeats)
		noStranger := noRepeat || full(strangers, o.Limits.Strangers)
		noUnchecked := noStranger || full(unchecked, o.Limits.Unchecked)
		// a reset is the first of its hour, or a repeat: decided now, as one that waited becomes
		// the first when the hour since the last one passes
		k := &claimed{}
		var class, sealed string
		err := tx.QueryRowContext(ctx, `
            SELECT id::text, kind, lang, sealed, attempts, c FROM (
                SELECT m.*, CASE WHEN m.class NOT IN ('account', 'repeat') THEN m.class
                    WHEN EXISTS (SELECT 1 FROM mail_outbox s WHERE s.to_hash = m.to_hash AND s.kind = m.kind AND s.id <> m.id
                         AND ((s.state = 'sent' AND s.finished_at > $2) OR (s.state = 'sending' AND s.lease_until >= $1))) THEN 'repeat'
                    ELSE 'account' END AS c
                  FROM mail_outbox m
                 WHERE ((m.state = 'ready' AND m.next_at <= $1) OR (m.state = 'sending' AND m.lease_until < $1)) AND m.expires_at > $1) q
             WHERE NOT ($3::boolean AND c = 'stranger' AND NOT checked) AND NOT ($4::boolean AND c = 'stranger') AND NOT ($5::boolean AND c = 'repeat')
             ORDER BY CASE c WHEN 'stranger' THEN 2 WHEN 'repeat' THEN 1 ELSE 0 END, next_at, id LIMIT 1`,
			now, now.Add(-time.Hour), noUnchecked, noStranger, noRepeat).Scan(&k.id, &k.kind, &k.lang, &sealed, &k.attempts, &class)
		if errors.Is(err, sql.ErrNoRows) {
			if noUnchecked {
				return o.waits(ctx, tx, now, ClassStranger, all.day, all.month)
			}
			return nil
		}
		if err != nil {
			return err
		}
		c = k
		it, ok := o.open(k.id, sealed)
		if !ok {
			o.Logger.Error("mail: a mail does not open (was the token secret changed?)", "mail", k.id, "kind", k.kind, "event", "mail_failed")
			return finish(ctx, tx, k.id, StateFailed, "unreadable", now)
		}
		still := time.Duration(0)
		if asked(k.kind) {
			// the account may have been closed or suspended, or confirmed, while its mail waited
			u, ok, err := o.wants(ctx, k.kind, it.To)
			if err != nil {
				return err
			}
			if !ok {
				return finish(ctx, tx, k.id, StateDropped, "", now)
			}
			if it.Msg == nil {
				m, wait, err := o.write(ctx, tx, k.kind, k.lang, u)
				if err != nil {
					return err
				}
				m.ID, it.Msg, still = k.id, &m, wait
				if sealed, err = o.seal(k.id, it); err != nil {
					return err
				}
			}
		}
		if it.Msg == nil {
			return finish(ctx, tx, k.id, StateFailed, "no mail", now)
		}
		k.attempts++
		expires := now.Add(still)
		// the send, and time to say so
		if _, err := tx.ExecContext(ctx, `UPDATE mail_outbox SET state = 'sending', sealed = $2, class = $3, lease_until = $4, attempts = $5,
            expires_at = CASE WHEN $6::boolean THEN $7 ELSE expires_at END WHERE id = $1::uuid`,
			k.id, sealed, class, now.Add(o.SendTimeout+30*time.Second), k.attempts, still > 0, expires); err != nil {
			return err
		}
		k.msg = it.Msg
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
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
	if c.msg == nil {
		return true, nil
	}
	// what happened to a claimed mail is kept even while the process stops
	keepCtx, cancelKeep := context.WithTimeout(context.WithoutCancel(ctx), o.SendTimeout+15*time.Second)
	defer cancelKeep()
	log := o.Logger.With("mail", c.id, "kind", c.kind, "lang", c.lang, "attempt", c.attempts)
	sendCtx, cancel := context.WithTimeout(ctx, o.SendTimeout)
	err = o.Mailer.Send(sendCtx, *c.msg)
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
