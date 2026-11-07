---
schema: 1
kind: pr
pr: 887
title: Buy on plugins.maktaba.tv, by choosing a linked home
date: 2026-11-07
session: 29e8f107-ee7f-4db2-a128-c02a2839240a
agent: claude-code
prs: [886]
---

## What happened
- A paid plugin's listing page has a plain Buy link to `/buy/<plugin>/home`. That page lists the signed-in account's linked homes in a form with no script. Its POST makes the same purchase intent the home's own Buy asks for, then continues on the existing price and pay pages, so there is one checkout path.
- The POST is allowed only from the store's own Origin (as `/pay` is), signed in, rate-limited, and for a home linked to this account now. Anything else makes no intent.
- The listing page's CSP stays as it was (`form-action 'none'`, one hashed script). Buy is a link, and the form lives on the buy pages, whose CSP already allows forms to this origin.

## What we learned
- Reusing the intent and its cookie kept the website's Buy from becoming a second checkout with its own rules. The helpers `newIntent` and `toBuyPage` are now the one place both paths go through.
- A worktree made for a second PR needs `npm ci` and the built `store.wasm`. Copying `build/store.wasm` from a worktree that has built it saves a cargo build.

## Keep doing
- Build a new entry point onto an existing flow's state (here the intent), not beside it.

## Open
- Apple snapshot references (#876).
