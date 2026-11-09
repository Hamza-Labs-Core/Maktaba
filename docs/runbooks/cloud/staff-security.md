# Runbook: a staff account's security alert

> Epic 50 stories 50.12, 50.33.

## When

`staff_lockout` (5 failed sign-in steps in 15 minutes locked the account for 30), `staff_recovery_code_used` (a sign-in spent a recovery code), `staff_passkey_counter` (a passkey answered with a signature counter that did not move forward, so a copy of its key may exist: the cloud disabled that passkey, ended every session of the account and refused the step; audit action `staff.passkey.counter_disabled`).

## Check

1. Ask the staff member whether it was them.
2. The audit rows (`actor_kind = staff`) in the service admin view, and the cloud's logs and error groups in the service admin's cloud log view ([the API's warnings, last 24 h](https://admin.maktaba.tv/logs?service=cloud.api&level=warn&last=24h); [Errors](https://admin.maktaba.tv/errors)) (Epic 53.17; the witness keeps a dead node's last lines).

## Do

1. It was them: nothing more. A lockout clears itself after 30 minutes, or at once with `maktaba-cloud staff unlock --email <email>`; after a recovery code, `staff reset-mfa` prints a new enrolment link with 10 fresh codes. After a passkey's counter alert, the passkey stays disabled even when it was them (a synced passkey that restored an old copy is the usual cause): if it was the account's only factor, `staff reset-mfa` prints a new enrolment link, where they add the passkey again.
2. It wasn't: disable the account at once on the leader, which ends every session (`sudo -u maktaba maktaba-cloud staff disable --email <email> --config /etc/maktaba/cloud.toml`), read what the account did in the audit, and rotate `MAKTABA_CLOUD_STAFF_TOKEN_SECRET` if a token may have leaked ([secrets-rotation.md](secrets-rotation.md)).

## Done when

The account is either confirmed or disabled, and the audit shows nothing done by anyone else.

## Tell users

Only if user data was touched: as [secrets-rotation.md](secrets-rotation.md) says.
