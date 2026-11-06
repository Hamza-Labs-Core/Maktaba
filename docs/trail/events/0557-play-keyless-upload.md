---
schema: 1
kind: pr
pr: 557
title: Keyless Google Play upload (Workload Identity Federation)
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge b2125c01. play-bundles.yml's Google Play upload uses Workload Identity Federation (GitHub OIDC) via google-github-actions/auth v2.1.13 @ c200f369, with `id-token: write` on the play job only. r0adkll/upload-google-play v1.1.5 @ e738b9dd reads the external_account credentials file through `serviceAccountJson` (its google-auth-library 9.15.1 supports it, checked at the sha).
- Gate: repo variables `PLAY_WIF_PROVIDER` + `PLAY_SERVICE_ACCOUNT`; the secret `PLAY_SERVICE_ACCOUNT_JSON` removed everywhere. Why: no downloaded key (Google warns against it).
- Google side set up the same day: project maktaba-513518 (number 714997343823), service account maktaba-play-upload with no key, pool/provider `github`, Workload Identity User for principalSet .../attribute.repository/Hamza-Labs-Core/Maktaba. Play Console grant is "Release apps to testing tracks" only (no production). Both variables set by the coordinator. Setup steps: docs/google-play.md "One-time setup".

## Keep doing
- Prefer keyless federation over downloaded service-account keys.
