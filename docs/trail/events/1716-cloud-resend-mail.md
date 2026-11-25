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

Three reviews found no leak and ten majors in all, fixed in the same PR. The first: strangers' sign-ups and resets could use up the day's mails (a reset of an account never confirmed counted as an account's); a stranger could lock a person out of password reset with a hard 10 a day; the records of processing named the sending region as where the mail is stored; and the public privacy page had no line for accounts or Resend. The second: a stranger could still lock a chosen person out and end their delivered link, the network limit was twice what it said and the reserve could be used up, and one privacy sentence was still untrue. The third: an attacker with confirmed accounts could still use up the reserve, the privacy texts lacked Turnstile, and a wrong Turnstile secret would have closed every web sign-in after a green deploy.

## What we learned
- Answering the same is not taking as long: forgot-password looked the account up and made a token only for an address that had one. The handler that only enqueues is provable without a clock: its test has no stores at all.
- A sign-up that fails on the unique email commits nothing, one that succeeds waits for a commit. `ON CONFLICT DO NOTHING` in one transaction with the outbox row commits in both.
- goose's `GetDBVersion` is the migration applied last, not the highest. A cloud migration numbered below the newest, applied late, makes `AtHead` (and so `/readyz`) false, in the previous release too when a deploy goes back (#1715). The whole suite was green with the low number: every test database is migrated from empty, in order.
- Production's smoke test registers `ci-e2e-<n>@example.com`. With a real driver each deploy would have bounced a mail, so a deployment that sends refuses reserved names (RFC 2606).
- `go test -tags integration` without `MAKTABA_CLOUD_DB_URL` skips every database test and says ok in 0.1 s. Read the time.

- A limit by day on a person's own reset is a lock-out for whoever asks first. The first fix (refuse only while the newest link is good) was still one: an ask that got no mail made a link and so ended the delivered one. What held is fewer rules: the link is made when its mail has room to go, and ends no other; using one ends the rest. Supersede, ordering and "the newest is good" all went with the rule that needed them.
- A token bucket of N that refills N an hour allows 2N−1 in the first hour. A number in the docs is a sliding window, or it is another number.
- A reserve must be as big as what it promises: 20 percent of 85 mails is 17, less than one person's reset an hour for a day. It is 30 now.
- A bot check that asks only requests with a web Origin is passed by leaving the Origin out. It can raise a share; it cannot be the gate, and two allowances add up.
- Count a requester's requests as they are asked, never the mails they cause: a limit that counts mails tells which addresses have accounts.
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
- Production without Turnstile's keys: strangers' mails stop at 40 percent of the day, and two networks can use that up. Four confirmed accounts of an attacker's own on five networks can begin to use up the whole day, a confirmed owner's reset included; with the keys, three or four on two networks, as a network's asks with and without the check add up. `cloud-ops.py reset-link` on the node is the owner's way in.
- A received mail's DKIM and DMARC, checked on QA after the merge (`docs/cloud-mail.md`, "Check").
