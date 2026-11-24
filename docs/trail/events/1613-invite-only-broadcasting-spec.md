---
schema: 1
kind: pr
pr: 1613
title: "spec(epic-50): broadcasting is invite-only; 50.36 closes the open links"
date: 2026-11-24
session: ab31266a-f317-49e9-896a-bcd2542713f1
agent: claude-code
---

## What happened
Home broadcasting changed to invitation only (decided 2026-11-24): a channel is visible only to the people its publisher invited. There are no open links, no public watch pages and no directory. #1613 updates only the spec:
- Epic 50 README §14 and §4: F14, F16, F18–F21.
- Stories 50.20, 50.22, 50.24 and 50.26–50.29.
- A new story, 50.36, with the inventory and the work to remove the `open` mode already built by 50.26 and 50.27.
- The Arabic for every changed wiki entry, the epic status (50.36 not started) and the epic wiki page.

The copyright, takedown and Report work (50.24 and the `maktaba.tv/legal/copyright` page) was written for open channels. Whether it stays is filed as decision `broadcast-invite-only-copyright`, with this recommendation: keep the designated agent, notice and takedown, and strikes, and replace the viewer Report button with *Leave channel*.

## What we learned
- **The open mode isn't just a flag.** It reaches the cloud and home migrations (`access IN ('invite','open')`), the signed access post to the edge (`OpenAllowed`, `Indexable`, `MaxSessionsPerIP`), the edge (`attest.ts`, `turnstile.ts`, `/api/v1/open/*`, `mkbo1.` tokens), both Broadcast access pages, and the Apple and Android guest pass kinds.
- **The edge renders pages without a pass.** `lineup` and `apiGuide` in `web/packages/broadcast-edge/src/site/routes.ts` fall back to `openChannels()` when a visitor has none, so the grid and guide render for anyone while an open channel exists. 50.36 closes this first: the edge deploys before the cloud.
- **The wiki's Arabic covers only each story's opening excerpt.** A change deeper in a story needs no new translation, but a changed title or opening does. `--ar-todo --epic 50` lists exactly which.

## Keep doing
- Running the code inventory in parallel with the spec edits, then checking its key claims in the source before writing them into a story.

## Open
- 50.36 (code) is not started.
- Decision `broadcast-invite-only-copyright` is unanswered. Gate item 4 and 50.24 depend on it.
- #1613 is held by the merge freeze.
