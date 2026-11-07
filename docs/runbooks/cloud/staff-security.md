# Runbook: a staff account's security alert

> Epic 50 stories 50.12, 50.33.

## When

`staff_lockout`, `staff_recovery_code_used`, `staff_passkey_counter`.

## Check

1. Ask the staff member whether it was them.
2. The audit rows (`actor_kind = staff`) in the service admin view, and the cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines).

## Do

1. It was them: nothing more (a new recovery code if one was used).
2. It wasn't: take the role away at once on the leader (`sudo -u maktaba maktaba-cloud admin revoke --email <email> --config /etc/maktaba/cloud.toml`; 50.33's staff accounts add `staff disable`), read what the account did in the audit, and rotate `MAKTABA_CLOUD_STAFF_TOKEN_SECRET` if a token may have leaked ([secrets-rotation.md](secrets-rotation.md)).

## Done when

The account is either confirmed or disabled, and the audit shows nothing done by anyone else.

## Tell users

Only if user data was touched: as [secrets-rotation.md](secrets-rotation.md) says.
