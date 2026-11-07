---
schema: 1
kind: pr
pr: 832
title: Plans Free, Basic and Pro; relay modes; usage by mode (story 50.19, part 1)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [808, 825]
---

## What happened
- Story 50.19, part 1 of 4:
  - the plan catalog (Free with no relay, Basic capped, Pro full quality), with the owner's numbers as TBD and `LaunchCheck` refusing paid plans while any is;
  - `ModeFor` and the meter counting bytes by mode into `relay_usage_monthly`; `Allow`/`ErrOverLimit` (the 402 on bytes) are gone;
  - migrations 00500006 (expand) and 00500007 (rename, marked contract);
  - the plans API, and the cloud web Billing page in en+ar;
  - maktaba.dev's Apps page.
- Parts 2–4: the relay's enforcement; the payment provider, usage reports and pay-as-you-go; the home and the rehearsal.

## What we learned
- Homes refuse a license whose tier they don't know (`validTiers`: free, home, pro). A tier rename is two releases: the home learns the new name (keeping the old as an alias), then the cloud issues it. Until then `basic` goes out as `home`.
- Widening a CHECK drops and re-adds the constraint, so `cloud-expand-only` calls it a contract, and the old release does misread the renamed rows during a deploy. Keep the rename in its own file, marked contract, apart from the expand-only table.
- `goose Down 1` rolls back the highest version, which is slot 0051, not the 0050 migration under test. Test a migration with `goose.DownTo(<version below it>)`.
- The runbooks lint (50.12) forbids `رصيد` (credit) in user-facing Arabic. Translate a plan's "allowance" as `حصة`. The story's Arabic for `billing.payg.desc` uses `رصيدك` and must change when part 3 adds it.
- From #808 after it merged: its `load-small` on CI with the new method gave five rounds from 0.000597 to 0.000608 CPU-seconds per Mbit (±1 %), and memory +0.7 %. The gate compared memory only that once (a baseline of the old method); main's next run is the CPU baseline.

## Open
- Parts 2–4 as above. The owner's prices, Pro's allowance and N, and the payment provider are on the go-live list.
