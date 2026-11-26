---
schema: 1
kind: pr
pr: 1769
title: Cloud readiness hardening — Resend webhooks, daily node watch, broadcast-off, and single-node rollout minors
date: 2026-11-26
session: 4419ee9d-62d2-421a-9d13-992d2c6e9711
agent: antigravity
---

## What happened
- Closed #1714 (Resend bounce and complaint webhook): added `POST /v1/mail/webhook/resend` verifying Svix HMAC-SHA256 signatures from a hot-reloaded secret file (`secrets/resend-webhook.key`), additive undoable migration `cloud/migrations/00580003_mail_webhooks.sql` (`mail_outbox.provider_id`, `mail_events`, `mail_suppressions`), hash-based suppression on hard bounces and complaints while continuing to permit `password_reset`, 30-day rate summaries in `maktaba-cloud mail status`, Prometheus alerts, runbook updates, and deploy secret wiring.
- Closed #1665 (daily single-node watch): added `.github/workflows/cloud-node-watch.yml` and `deploy/cloud-relay/ops/node-watch.py` checking TLS expiry (< 14 days), `/readyz` on relay and API, and over SSH with a pinned host key, newest backup age (> 26 hours) and `ops/backup.sh --check`, while keeping `cloud-probe.yml` untouched.
- Closed #1660 (broadcasting-off minors): replaced heuristic route discovery with an explicit `(listener, method, pattern)` classification table in `cloud/cmd/maktaba-cloud/broadcast_routes_test.go`, tightened `openWhileOff` in `web/packages/broadcast-edge/src/worker.ts` to require configured `INGEST_HOST`, and replaced the 404 banner on `web/admin/src/pages/Moderation.tsx` with a localized English and Arabic empty state.
- Closed #1707 and #1734 (single-node rollout minors): updated `maktaba-rollout status` for failed-first nodes, cleaned `pending.new` on partial `begin` failure, bounded Docker probes and container shutdowns with `DOCKER_INFO_TIMEOUT` during rollback against a frozen (`SIGSTOP`) Docker daemon, handled `unhold` on stopped non-first deploys, and added rehearsal cases and mutations for `begin`'s `heal_docker`, boot's `stop_held`, and frozen Docker rollback.
- Merged as `c00c894d0` with all 174 checks green.

## What we learned
- Docker 29's half-removed container state (`docker ps -aq` lists an ID whose `docker inspect` returns `No such container`) only reproduces in the Ubuntu 26.04 KVM VM (`reboot_rehearsal.py`), not on Ubuntu 24.04 runners (`rollout_rehearsal.py`).
- Inside the VM, `systemctl mask maktaba-relay.service` fails because `/etc/systemd/system/maktaba-relay.service` is the unit file itself; `systemctl disable` across `power_cut()` cleanly suppresses boot's `heal_docker` so `begin`'s `heal_docker` is exercised in isolation, with `systemctl enable` restored in `finally:`.
- `stop_all()` on a `SIGSTOP`-frozen Docker daemon must short-circuit on the first failed/timed-out probe rather than iterating 60 times, and `docker stop`/`logs`/`rm`/`volume ls` need explicit `timeout -k 5` bounds so rollback finishes well inside the rehearsal budget.
- Integration tests asserting latest rows ordered by timestamp (`TestWebhookEventsSuppressAndAlert`) should advance mock timestamps explicitly and look up records by key rather than assuming UUID tie-breaking order.

## Keep doing
- Put Docker 29 / real-boot rehearsal cases and mutations in `reboot_rehearsal.py` (`reboot-mutation`) when they depend on the production kernel and Docker version.
- Run `shellcheck -S warning` on every changed shell script before pushing (`SC2015` breaks `single-node` rehearsal jobs early).

## Open
- None for #1714, #1665, #1660, #1707, or #1734 (all closed).
