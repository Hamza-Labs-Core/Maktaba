# Maktaba Cloud's mail

> The account mails (email confirmation, password reset, "this address already has an account",
> payment and limit notices, copyright mails) go through [Resend](https://resend.com), from
> `Maktaba <no-reply@maktaba.tv>`, with replies to `support@maktaba.tv`. A cloud without a
> provider's key writes them to the api's log instead (`mail.driver = "log"`, the default).

## How a mail is sent

1. A request (register, resend, forgot password) writes one sealed row in `mail_outbox` and
   answers. It looks no account up and waits for no mail, so neither the answer nor its time says
   whether the address has an account. A sign-up's account and its row are one transaction.
2. The api role works the rows: it finds the account, makes the link, writes the mail in the
   request's language, else the account's (English or Arabic, plain text and HTML, nothing loaded
   from elsewhere, no tracking).
3. It hands the mail to the provider: 10 s an attempt, at most three, only a 429, a 5xx or a lost
   answer tried again, with the row's id as the idempotency key. A mail not taken stays in the
   outbox and is tried again after 30 s, 2, 10 and 30 minutes, then each hour, until it is too old
   to be of use (half of its link's life: 12 hours for a confirmation, 30 minutes for a reset; 23
   hours for a notice, as Resend forgets the key after 24). A mail whose account was closed,
   suspended or confirmed meanwhile, or that a newer one of its kind replaced, is not sent.

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
| `allow_recipients` | the only addresses and `@domain`s this deployment writes to; empty is everyone. Another address's mail is written and given up (`suppressed`) |
| `subject_prefix` | before each subject (`[QA]`) |
| `daily_limit`, `monthly_limit` | mails sent in 24 hours and in 30 days (80 and 2,700) |

The api role refuses to start on `resend` without a key, a sender, an https `server.web_url` (its
links' pages) and limits above 0, or with `server.env = "staging"` and no `allow_recipients`.

## The limits

| Limit | What |
|---|---|
| per IP | 5 requests, then one each 12 s (register, resend, forgot password) |
| per network (IPv4 /24, IPv6 /56), all addresses together | 10 requests at once and 10 an hour, 40 a day; `429`, counted as they are asked (a deployment that sends) |
| per address: reset | 3, then one each 15 minutes, with no limit by day: one refused leaves a newest link with at least 45 minutes left. An address nobody confirmed: 2, then one an hour |
| per address: the others | confirmation: 3, then one each 15 minutes, 6 a day; "already registered": 1 an hour, 3 a day; a notice: 20 an hour, 40 a day |
| per day and 30 days | `daily_limit`, `monthly_limit`. Mails to addresses nobody confirmed (confirmations, "already registered", resets of an account never confirmed) may use 60 % of each; they and the repeats (a confirmed account's fourth reset of the day and on) 80 %. The rest is kept for confirmed accounts |

Over the day's or the month's limit, mails wait and then expire; confirmed accounts' go first. Over
Resend's own quota (429 `daily_quota_exceeded`, `monthly_quota_exceeded`) a mail waits an hour
between tries. Registering and signing in go on either way. The log says `mail_limit_reached` or
`mail_provider_refused`; an operator can confirm a waiting sign-up by hand
([`cloud-single-node.md`](cloud-single-node.md), "Operator actions"). Resend's free plan is 100
mails a day and 3,000 a month for the whole account, QA's included: the two targets' limits
(`deploy/cloud-relay/targets/*.env`) add up to 95 and 2,900, which leaves a little for mail sent
from Resend's dashboard. A bigger plan is bought at Resend first, then
the targets' limits go up.

## The single node (QA and production)

Each GitHub environment, `qa` and `prod`, has its own `RESEND_API_KEY` secret. The deploy hands it
to the node on stdin; `setup.sh` writes `secrets/resend.key` (0600, the cloud user's) and turns the
driver to `resend` when there is a key, the target's `MAIL_ALLOW` says whom the node may write to
(`everyone`, or a list) **and** the release has the driver (`maktaba-cloud mail drivers`, asked of
its image). Without any of them the driver stays `log`, and the deploy says so (production without
the secret: a warning). A deploy after the secret is removed takes the key off the node, and a
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
| `mail_address_limit` in the log | too many of one kind to one address: dropped, the last link stays good |
| state `logged` | the log driver took it: not delivered, and not counted |
| state `superseded` | a newer mail of its kind took its place, or its account no longer wants it |
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

Not built: Resend's bounce and complaint webhook
([#1714](https://github.com/Hamza-Labs-Core/Maktaba/issues/1714)). Until then Resend's dashboard
shows them, and it stops sending to an address that bounced or complained by itself.
