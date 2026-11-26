### Added

- **Resend bounce and complaint webhook.** Maktaba Cloud now verifies Svix-signed delivery webhooks
  from Resend (`POST /v1/mail/webhook/resend`), suppresses future sign-up and notice emails to
  hard-bounced or complaining address hashes while still allowing password resets, surfaces counts
  and rate alerts in `maktaba-cloud mail status`, and wires the webhook secret per environment.
- **Daily single-node health watch.** Added a daily GitHub Actions watch workflow
  (`.github/workflows/cloud-node-watch.yml`) that checks TLS expiry (< 14 days) across the single
  node's public names, verifies `/readyz` on relay and API, and checks backup age (> 26 hours) and
  `ops/backup.sh --check` over SSH with a pinned host key.

### Fixed

- **Broadcasting-off hardening across cloud, edge, and console.** Replaced heuristic route discovery
  in the cloud broadcast-off test with an explicit `(listener, method, pattern)` classification
  table, tightened the broadcast edge's off-mode ingest check to require the configured ingest
  hostname, and replaced the 404 error banner on the operator console's Moderation page with a clean
  broadcasting-off state in English and Arabic.
- **Single-node rollout diagnostics and stuck-Docker protection.** `maktaba-rollout status` now
  explains why a failed-first node will not start on its own and how to start it intentionally,
  failed `begin` runs clean up `pending.new`, `docker info` and Docker probes during rollback are
  bounded by a timeout so a frozen daemon fails promptly, and `unhold` on a stopped non-first deploy
  clears `no-release`.
