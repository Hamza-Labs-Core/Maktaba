---
schema: 1
kind: pr
pr: 1067
title: Staff passkeys for the operator console (story 50.33, part 2)
date: 2026-11-09
session: aa4f6da6-177c-478d-81fa-7ab103e7cac3
---

## What happened
- #1067 merged at fbe622a7d. An operator can sign in with a passkey instead of, or alongside, TOTP; the console offers the passkey first when the account has one. A passkey also satisfies the fresh step-up that suspend, block and the other sensitive actions require. A new operator can enrol with a passkey only, and enrolment cannot finish without a second factor.
- Each passkey ceremony's challenge is single-use and expires after 5 minutes. Migration `00500014` holds passkey records and challenges.
- A passkey whose sign counter goes backwards (a cloned key) is disabled, the step is refused, every session of that account ends (refresh cookie included), and a `staff_passkey_counter` alert fires. The session-revocation part came from review; the end-to-end test checks the cloned key's own session is refused afterwards.
- The new step-up routes are enforced server-side with a role test (no token, customer token, token without a second factor all refused). Console strings ship in English and Arabic.
- A simulated authenticator (`cloud/internal/staffauth/webauthntest`) covers the spec's T05 cases: register, sign in, wrong RP, counter backwards, no user verification.
- After #1027 merged, main merged cleanly into the branch. lint (cloud) then failed on SA1019: the test authenticator read `ecdsa.PublicKey.X/Y`, deprecated since Go 1.26. It now reads `PublicKey.Bytes()` and slices the uncompressed point (039471fb7).

## What we learned
- The delegate's local Docker stopped, so the new role test and the extended integration test first ran in CI. CI's cloud integration job is the real gate for these; it passed.
- A delegated agent here cannot wait on PR checks, so stacked PRs (part 2 on part 1) need the coordinator to drive merges in order.
- Go 1.26 deprecates raw `ecdsa` coordinates; use `PublicKey.Bytes()` / `ecdsa.ParseUncompressedPublicKey`.

## Keep doing
- Revoke sessions, not just the credential, when a credential looks cloned.

## Open
- The customer auth handler still computes its argon2id timing hash at init (see the #1027 event).
