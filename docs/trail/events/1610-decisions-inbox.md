---
schema: 1
kind: pr
pr: 1610
title: "Decisions for the owner go through one inbox, for every agent"
date: 2026-11-24
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
agent: claude-code
---

## What happened
- Decisions reached the owner in scattered ways: chat questions, local file paths to images
  that never displayed, and one-off review pages without a way to answer on them.
- #1610 adds CLAUDE.md rule 9, an AGENTS.md note and `docs/decisions.md`: every agent, on every
  provider, files a decision with `~/.maktaba/decisions/bin/ask` and reads the answer with
  `bin/answer`. The owner answers all of them on one claude.ai page with options, notes and a
  submit button; a scheduled session syncs the inbox with the page every 15 minutes.

## What we learned
- A decision the owner can't see in full (images, context, the recommended option) isn't
  answerable; visual approvals need full-size, zoomable images on the same page as the choice.
- A shared local inbox works for agents of any provider on this machine; only the sync needs
  Claude's artifact tools.

## Keep doing
- One recommended option per decision, with the context and images the owner needs.

## Stop doing
- Asking the owner to decide in chat, in a pull request, or on a page with no way to answer.

## Open
- Agents running off this machine can't reach the local inbox; the store stays local by choice.
- The sync depends on a Claude session; when it can't run it writes `SYNC-FAILED.txt`.
