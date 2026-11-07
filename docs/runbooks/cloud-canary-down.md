# Runbook: remote play is down (the canary or the API fails)

> Alerts `canary_down`, `api_down`, `slo_burn_fast`, `slo_burn_slow`, `held_requests_timing_out`
> (Epic 50 story 50.10). Budget: 43.8 min a month (99.9 %).

1. **Is it us or the probe?** Both networks (the witness and GitHub's runners) fail together for a
   real outage. One alone: look at that network first.
2. **Who leads?** `patronictl -c /etc/patroni/patroni.yml list` on A or B. A failover in progress
   takes up to 3 minutes by itself: wait for it (`failover_happened` says when it started).
3. **Does the Floating IP point at the leader?** `fip_mismatch` says no: `docs/runbooks/cloud-dns-swap.md`.
4. **Is the relay running on the leader?** `systemctl status 'maktaba-cloud-relay@*'`,
   `curl -s http://127.0.0.1:8443/readyz` (or 8453 for green). `draining` means a deploy is moving
   homes; `db_unreachable` after 5 minutes means the database is away too.
5. **Is the canary home itself up?** On W: `systemctl status maktaba-canary`.
6. Post on the status page (50.11) once it has lasted 5 minutes, and write the postmortem.
