---
schema: 1
kind: pr
pr: 1716
title: "Maktaba Cloud sends its account mails through Resend, from an outbox"
date: 2026-11-25
session: 7185a26f-424c-4774-a4e3-8d906d44792b
agent: claude-code
---

## What happened
PR #1716 gives the cloud a real mail driver (Resend over `net/http`) behind a durable outbox (`cloud/internal/mail/outbox`, table `mail_outbox`). Register, resend and forgot-password only write one sealed row and look no account up; the api role makes the link and sends. Templates are English and Arabic. The node's key comes from its GitHub environment's `RESEND_API_KEY`; without it the driver stays `log`. QA writes to its own list only. No real mail was sent before the merge.

The review found no leak and four majors, fixed in the same PR: strangers' sign-ups and resets could use up the day's mails (a reset of an account never confirmed counted as an account's); a stranger could lock a person out of password reset with a hard 10 a day; the records of processing named the sending region as where the mail is stored; and the public privacy page had no line for accounts or Resend.

## What we learned
- Answering the same is not taking as long: forgot-password looked the account up and made a token only for an address that had one. The handler that only enqueues is provable without a clock: its test has no stores at all.
- A sign-up that fails on the unique email commits nothing, one that succeeds waits for a commit. `ON CONFLICT DO NOTHING` in one transaction with the outbox row commits in both.
- goose's `GetDBVersion` is the migration applied last, not the highest. A cloud migration numbered below the newest, applied late, makes `AtHead` (and so `/readyz`) false, in the previous release too when a deploy goes back (#1715). The whole suite was green with the low number: every test database is migrated from empty, in order.
- Production's smoke test registers `ci-e2e-<n>@example.com`. With a real driver each deploy would have bounced a mail, so a deployment that sends refuses reserved names (RFC 2606).
- `go test -tags integration` without `MAKTABA_CLOUD_DB_URL` skips every database test and says ok in 0.1 s. Read the time.

- A limit by day on a person's own reset is a lock-out for whoever asks first. Refuse only while the newest link is still good (3, then one each 15 minutes, for a link of an hour), and protect the day with shares by whom a mail goes to, decided when it is worked on.
- Count a requester's requests as they are asked, never the mails they cause: a limit that counts mails tells which addresses have accounts.
- "The newest" needs an order of its own: two requests of one microsecond, ordered by a random id, made the good link look stale. The outbox numbers each mail as it is written, under its lock.
- A provider's region says where mail is sent from, not where it is kept. Resend stores in the USA for 30 days whatever the region.
- `NotCollected: "IP addresses"` was true of the relay's analytics and false of accounts (sessions hold one). A policy's line needs the scope it is true in.

## Keep doing
- Number a cloud migration above the newest until #1715 is in every deployed release, and try it on a database at the previous head.
- Run the deploy's shell in a test: `test_cloud_deploy.py` runs setup.sh's mail block and the workflow's setup step with an ssh of its own.

## Open
- #1731: security records have no retention, and the stores' privacy answers leave out Cloud sign-in.
- #1722: the rehearsal's "a power cut in apply" fails now and then, on main too.
- #1714: Resend's bounce and complaint webhook.
- #1715: `AtHead` by the applied set.
- The Phase 1 pair (`deploy/cloud-ha`) is not given the key: its driver is `log`.
- A received mail's DKIM and DMARC, checked on QA after the merge (`docs/cloud-mail.md`, "Check").
