---
schema: 1
kind: pr
pr: 734
title: The open CodeQL alerts on main, fixed or dismissed
date: 2026-11-06
session: 60b0cc60-c7a5-4b57-b926-2b55ba207362
---

## What happened
- Twelve CodeQL alerts were open on main. Fixed: the diagnostics export forwarded the caller's raw query to the internal log peers (16); the update status wrappers relayed an undecorated body with no content type (37, 150); node-local sign-in cookies were Secure only by environment (39, 40); Settings → Models kept a typed OpenAI key in plain localStorage and never used it (36, with an advisory in `docs/security/advisories/`).
- Taught CodeQL instead of dismissing: uninstall printed credential file paths from a variable named `passwords` (55, 56: renamed); stream rendition and segment names were already confined by `CanonicalUnder`/`EnsureUnder` (89, 105: an explicit `..` refusal added).
- Dismissed as false positives: the library root checks (1, 2), admin-only by design, where any absolute folder is a valid root.

## What we learned
- CodeQL's Go path model does not follow `CanonicalUnder`'s prefix check; a `strings.Contains(x, "..")` guard on the URL parameter is what it recognises.
- Its clear-text-logging heuristic matches variable names: a list of credential file paths named `passwords` reads as passwords.
- A wrapper that relays a captured response must set its own non-HTML content type; copying the captured headers is invisible to the query and leaves a typeless body for the browser to sniff.
- Dismissal comments are capped at 280 characters.
- maktaba.dev never publishes `docs/security/advisories/` (the devportal build's `NEVER` list).

## Keep doing
- Write a test that fails on main for each real alert before calling it fixed.

## Open
- Whether each alert closes depends on CodeQL's re-run on main after the merge.
