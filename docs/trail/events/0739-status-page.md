---
schema: 1
kind: pr
pr: 739
title: The status page, status.maktaba.tv, and its feed from the probes
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [727]
---

## What happened
- Story 50.11, part 1: `status/` (a dependency-free Node builder: English and Arabic pages, uptime bars, posts, `summary.json`), `status-page.yml` (GitHub Pages, off until `STATUS_PAGE`), the outside probe writing each round to the `status-data` branch beside the witness's own view (`bin/status-export`), the witness's one public site behind a token (`Caddyfile.witness`), the rehearsal's `status-feed` scenario, a runbook, and the maktaba.dev cloud guide's status section in both languages.

## What we learned
- Probe data does not belong on main: `auto-tag.yml` tags every green commit of main, so a probe writing every few minutes would cut a release each time. A data branch keeps main's history and releases clean.
- A push made with `GITHUB_TOKEN` starts no workflow, but `gh workflow run` (workflow_dispatch) with that token does, given `actions: write`. That is how the probe starts the page's build at once on a state change, instead of waiting for a schedule that GitHub may run late.
- GitHub Pages sends its own `Cache-Control: max-age=600`, and no file can change it. A client that needs fresher data adds a per-minute query (`?t=<unix minute>`).
- macOS's bash 3.2 treats `"${arr[@]}"` of an empty array as unbound under `set -u`; `${arr[@]+"${arr[@]}"}` works in both.
- #727 named a witness site for the Pushgateway that nothing set up; this PR adds it. When an earlier PR says something is reachable, check that something actually serves it.
- On an RTL page, a row of day cells and its axis labels must flow the same way. Forcing the cells to `direction: ltr` put "90 days ago" over today; letting them follow the page's direction fixed it. Look at the Arabic page at phone width before you push.

## Open
- 50.11 part 2: the banners in web, cloud web, Apple and Android (53.06's status strip draws them; the fetchers and strings are this story's).
- Notifications reads "No measurement yet" until 53.17 emits `push_outbox_lag_seconds`; the held-request rate needs `maktaba_relay_requests_total` on the witness, which 50.10 scrapes.
- 50.12 to 50.15, 50.19, 50.30 to 50.35, 53.16, 53.17 and 53.18 follow.
