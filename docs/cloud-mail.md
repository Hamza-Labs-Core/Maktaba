# Maktaba Cloud's mail

> The account mails (email confirmation, password reset, "this address already has an account",
> payment and limit notices, copyright mails) go through [Resend](https://resend.com), from
> `Maktaba <no-reply@maktaba.tv>`, with replies to `support@maktaba.tv`. A cloud without a
> provider's key writes them to the api's log instead (`mail.driver = "log"`, the default).

## How a mail is sent

1. A request (register, resend, forgot password) writes one sealed row in `mail_outbox` and
   answers. It looks no account up and waits for no mail, so neither the answer nor its time says
   whether the address has an account. A sign-up's account and its row are one transaction.
2. The api role works the rows: is there an account that wants the mail, and was none of its kind
   sent to the address a moment ago? A row nothing comes of keeps no address and goes after a day.
3. When there is room within the limits, and only then, it makes the link and writes the mail, in
   the request's language, else the account's (English or Arabic, plain text and HTML, nothing
   loaded from elsewhere, no tracking).
4. It hands the mail to the provider: 10 s an attempt, at most three, only a 429, a 5xx or a lost
   answer tried again, with the row's id as the idempotency key. A mail not taken stays in the
   outbox and is tried again after 30 s, 2, 10 and 30 minutes, then each hour, until it is too old
   to be of use (half of its link's life: 12 hours for a confirmation, 30 minutes for a reset; 23
   hours for a notice, as Resend forgets the key after 24). A mail whose account was closed,
   suspended or confirmed meanwhile is not sent.

**Links.** A reset's link is good for an hour, a confirmation's for a day. Asking for another never
ends one: each sent is good for its own life, until one is used, which ends the others of its
account and kind. A password change and signing out everywhere end the reset links too. So nobody can end the link in somebody's inbox by asking.

A restart, a deploy or a crash loses no mail: the next process sends the row, with the same
idempotency key. Several api nodes share the outbox. The worker only writes rows.

A row's contents are sealed under a key derived from `MAKTABA_CLOUD_TOKEN_SECRET` and cleared once
the mail is sent or given up; the address is kept as a keyed hash; the row goes after 35 days. The
log names a mail by its row's id, kind, language and outcome: never an address, a link, a subject or
the key. (The log driver is the exception: it writes the mail, for development; in production the
log's redaction takes the address and the link's code out.)

Rotating `MAKTABA_CLOUD_TOKEN_SECRET` makes the mails still waiting unreadable: they are marked
failed, and people ask again.

## Settings

`[mail]` in `cloud.toml` ([`cloud.example.toml`](../cloud/configs/cloud.example.toml)):

| Key | What |
|---|---|
| `driver` | `log` (default) or `resend` |
| `from`, `reply_to` | the sender, on the domain verified at Resend; where answers go |
| `resend_api_key_path` | the key's file, read at each send (a new key needs no restart). Or env `MAKTABA_CLOUD_RESEND_API_KEY_FILE`, or the key itself in `MAKTABA_CLOUD_RESEND_API_KEY`. A key written in `cloud.toml` is refused |
| `resend_webhook_secret_path` | Resend's Svix signing secret (`whsec_...`) for `POST /v1/mail/webhook/resend`, read on each delivery (no restart on rotation). Or env `MAKTABA_CLOUD_RESEND_WEBHOOK_SECRET_FILE` / `MAKTABA_CLOUD_RESEND_WEBHOOK_SECRET`. Unset or missing file: the webhook answers 404. Written in `cloud.toml`: refused |
| `allow_recipients` | the only addresses and `@domain`s this deployment writes to; empty is everyone. Another address's mail is written and given up (`suppressed`) |
| `subject_prefix` | before each subject (`[QA]`) |
| `daily_limit`, `monthly_limit` | mails sent in 24 hours and in 30 days (80 and 2,700) |

The api role refuses to start on `resend` without a key, a sender, an https `server.web_url` (its
links' pages) and limits above 0, or with `server.env = "staging"` and no `allow_recipients`.

## The limits

| Limit | What |
|---|---|
| per IP | 5 requests, then one each 12 s (register, resend, forgot password) |
| per network (IPv4 /24, IPv6 /56), all addresses together | 10 asks in any hour, 20 in any day; with the bot check passed 40 and 60, counted apart. `429`, with when to come back. A request that asks nothing (no address, a weak password, a failed check) does not count |
| per address and kind | a reset or a confirmation each 15 minutes (a confirmation 6 a day); a reset of an account nobody confirmed each hour; "already registered" each 8 hours. One asked sooner is dropped: the one sent before still works, but for an account nobody confirmed it may be near its hour |
| per day and 30 days | `daily_limit`, `monthly_limit`, and of them: mails to addresses nobody confirmed 60 % (40 % for those asked for without the bot check); they and the repeats (a confirmed account's reset within an hour of one sent) 70 %. The rest, 26 of 85 a day, is for the first reset of each hour of confirmed accounts, and notices |

Over the day's or the month's limit, mails wait and then expire; confirmed accounts' go first. Over
Resend's own quota (429 `daily_quota_exceeded`, `monthly_quota_exceeded`) a mail waits an hour
between tries. Registering and signing in go on either way. The log says `mail_limit_reached` or
`mail_provider_refused`; an operator can confirm a waiting sign-up by hand
([`cloud-single-node.md`](cloud-single-node.md), "Operator actions"). Resend's free plan is 100
mails a day and 3,000 a month for the whole account, QA's included: the two targets' limits
(`deploy/cloud-relay/targets/*.env`) add up to 95 and 2,900, which leaves a little for mail sent
from Resend's dashboard. A bigger plan is bought at Resend first, then
the targets' limits go up.

### The bot check, and what is left

With Turnstile's keys the web app's sign-up, reset, resend and sign-in pages ask for its check, and
a request from a web page without it is refused. A request from no web page (an app, a script) is
not asked: it gets the smaller shares above. **Without the keys every request does**, so
strangers' mails stop at 40 % of the day.

What a determined attacker can still do, within a plan of 100 mails a day:

- **Strangers' mails**, without the keys: about two networks a day use up the share asked for without
  the check, and nobody new gets a confirmation that day. With them, sign-ups from the web pages go on.
- **The whole day**, a confirmed owner's reset included: each confirmed account of the attacker's
  own draws a reset an hour from the reserve. Without the keys, four accounts and five networks
  start to; **with them, three or four accounts and two networks do**, as a network's asks with and
  without the check add up (80 of an 85-mail day). The keys protect sign-ups, not this. Links already
  sent still work, and **the owner always has a way in**: `cloud-ops.py reset-link` on the node
  ([`cloud-single-node.md`](cloud-single-node.md), "Operator actions").
- **A shared network** (CGNAT, a school): its asks used up, the others there wait up to a day. With
  the keys the web pages' own allowance stays open.
- **One bit a day**: someone who fills the day to one below a limit, then asks for an address, can
  tell from their own next mail whether it had an account. Noisy.

The remedy for the first two is a bigger plan. Known and left: a mail tried again can arrive with a
link that another, used meanwhile, has ended; a Turnstile token is not bound to one page's action.

**The keys**: Cloudflare → Turnstile → a widget in managed mode for the web app's name
(`app.maktaba.tv`; QA's `staging-app.maktaba.dev`), one for each. `gh secret set
TURNSTILE_SECRET_KEY --env prod` and `TURNSTILE_SITE_KEY` the same, pasted at the prompt (QA's with
`--env qa`), then deploy: `setup.sh` asks Cloudflare once
whether it knows the secret, and fails if it does not. Without both the check is off, and the deploy says so. The node's operator
console has no public name and is left out (`turnstile.staff = false`).

## The single node (QA and production)

Each GitHub environment, `qa` and `prod`, has its own `RESEND_API_KEY` secret. The deploy hands it
to the node on stdin; `setup.sh` writes `secrets/resend.key` (0600, the cloud user's) and turns the
driver to `resend` when there is a key, the target's `MAIL_ALLOW` says whom the node may write to
(`everyone`, or a list) **and** the release has the driver (`maktaba-cloud mail drivers`, asked of
its image; an image that cannot be asked fails the deploy before anything is rolled out). Without
any of them the driver stays `log`, and the deploy says so (production without the secret: a
warning). A deploy after the secret is removed takes the key off the node, and a
`MAIL_` line removed from a target's file is removed there.

- **Production**: `MAIL_ALLOW=everyone`. Like every deployment that sends, it never mails a name
  nobody has (`example.com`, `.test`, `.invalid`…): its smoke test's accounts are such.
- **QA** writes only to `@maktaba.tv` and Resend's own test inboxes, with `[QA]` in the subject. Its
  tests' accounts (`ci-e2e-*@example.com`) are not on the list: the tests confirm them with
  `cloud-ops.py verify-email`, and fail if QA sends with no list.

### Make the key

1. Resend → API keys → Create: permission **Sending access**, domain **maktaba.tv**. One key for QA
   and one for production, so either can be revoked alone.
2. `gh secret set RESEND_API_KEY --env qa` (and `--env prod`), pasted at the prompt. Never as a
   repository secret, on a command line or in a file of the repository.
3. Deploy (Actions → Cloud deploy). With a release from before the driver the node keeps `log`.

**Rotate**: make a new key, set the secret, deploy, check (below), then revoke the old one at Resend.
The node's nightly backups hold the old key for 14 days (`keys-*.tar.gz`, root only), so rotating
is done only once the old one is revoked.
**Stop mail**: delete the environment's secret and deploy: the driver is `log` again. At once:
revoke the key at Resend; mails wait in the outbox until theirs is too old.

### DNS

On maktaba.tv, as Resend's Domains page shows them (region eu-west-1), all DNS only:

| Name | Type | What |
|---|---|---|
| `resend._domainkey` | TXT | Resend's DKIM key |
| `send` | MX, TXT | the bounce address and its SPF |
| `rsend` | CNAME | Resend's own |

Open and click tracking are off for the domain, and stay off. **Receiving is at Proton**: the
domain's own MX, SPF, DKIM (`protonmail*._domainkey`) and DMARC records are Proton's and are not
changed for Resend, whose records are under other names and align with DMARC by DKIM and by the
`send` subdomain.

### Check

```bash
curl -fsS https://api.maktaba.tv/readyz      # "mail": driver, delivers, recipients
/opt/maktaba-relay/ops/cloud-ops.py mail-status                                # on the node: limits, states, last refusals
echo 'you@maktaba.tv' | /opt/maktaba-relay/ops/cloud-ops.py mail-test ar       # one test mail (en without a language)
docker compose --env-file .env logs api | grep -E '"event":"mail_'             # in /opt/maktaba-relay
```

`mail-test` goes the whole way (the outbox, the api, Resend) and prints what became of it. On QA use
an address on its list. Then look at the received mail's headers: `dkim=pass` for `maktaba.tv` and
`dmarc=pass`.

| What is seen | What it is |
|---|---|
| `401`, `403`, `400 invalid_api_key` (`mail_provider_refused`) | the key is wrong, malformed, revoked or suspended: set the secret and deploy. Mails wait |
| `403 validation_error` | the domain or the sender is not verified at Resend |
| `429 rate_limit_exceeded` | more than Resend takes a second; tried again by itself |
| `429 daily_quota_exceeded`, `monthly_quota_exceeded` | the plan is spent (above) |
| `422 …` (`mail_failed`) | that one mail is refused; it is not tried again |
| state `dropped` | no account wants it, or (`mail_address_limit` in the log) one was sent to the address a moment ago |
| state `logged` | the log driver took it: not delivered, and not counted |
| state `suppressed` | the recipient is not on this node's list (QA), or its domain can belong to nobody (`example.com`) |
| state `failed`, `unreadable` | the token secret changed while the mail waited |

## Where the mails are

Resend sends from Ireland (eu-west-1) and [stores in the United
States](https://resend.com/docs/dashboard/domains/regions) whatever the region: the address, the
mail with its link, its events and its logs, for [30 days](https://resend.com/security/gdpr), and in
backups 7 days more; an address that bounced or complained stays on its suppression list. The
transfer is under the EU Standard Contractual Clauses in its [DPA](https://resend.com/legal/dpa) and
its EU-U.S. Data Privacy Framework listing. What we keep is above (How a mail is sent). The public
privacy page, `CurrentPolicy()`, the record of processing and the DPA template say the same; a
change of provider, region or plan changes them in the same pull request.

## Bounce and complaint webhook

The api listens at `POST /v1/mail/webhook/resend` for Resend's delivery events (`email.bounced`,
`email.complained`, `email.delivery_delayed`, `email.failed`):

- Svix headers (`svix-id`, `svix-timestamp`, `svix-signature`) are verified against the signing
  secret in `secrets/resend-webhook.key` (`RESEND_WEBHOOK_SECRET` per GitHub environment, handed
  over on stdin like `RESEND_API_KEY`). Without a secret, the route answers 404.
- Events are idempotent by `svix-id` and match `mail_outbox` by Resend's message id (`provider_id`),
  never storing recipient addresses in the clear.
- On a hard bounce (`Permanent`) or a complaint, the address's keyed hash is recorded in
  `mail_suppressions`: future sign-up and notice mails to that hash are marked `suppressed`, while
  password resets are still attempted so nobody is locked out.
- When 30-day rates near Resend's limits (hard bounces 3 % toward 4 %, complaints 0.06 % toward
  0.08 %), the api logs `mail_bounce_rate_high` / `mail_complaint_rate_high` (`maktaba_errors_total`
  and `docs/runbooks/cloud/mail-reputation.md`) and `maktaba-cloud mail status` reports the counts,
  rates, and active alerts.
