---
schema: 1
kind: pr
pr: 674
title: The trail checker sees more secret shapes; the hook reads prs: from git history
date: 2026-11-06
session: f43ebb40-6f2c-4bbc-984a-059b86779c63
prs: [655]
---

## What happened
- Review fixes for #655. The credential rule used `\b` before the key, which never matches after `_`, so `POSTGRES_PASSWORD=…` and `MAKTABA_PG_PASSWORD="…"` passed; JSON keys, `token=`, `github_pat_`, `ASIA` keys, Bearer tokens and non-Postgres URLs with a password passed too. All are refused now, and a plain-word value (`password: regenerated`) or a reference (`$X`, `<x>`) is not.
- The hook read `prs:` only from checked-out files, so a PR covered by another event's `prs:` on an unchecked-out branch kept blocking Stop. It now reads `prs:` from `git log --all -p` over the events.
- A hung `git fetch` raised `TimeoutExpired` before the stamp was touched, so every Stop waited 15 s again; the stamp is written first now.
- Snapshots: a stray file in `snapshots/` crashed `check` with a `ValueError`; `#N` for an issue failed as an unknown citation (`issue #N` now cites nothing); `skeleton --tag` built on the newest snapshot even for an older tag.

## What we learned
- The credential rule is a heuristic: an unquoted value of letters only is read as prose, so an all-letters password written unquoted still passes. Quoted values and anything with a digit or symbol are refused.
- The repository's 268 events had no hit under the wider rules.
