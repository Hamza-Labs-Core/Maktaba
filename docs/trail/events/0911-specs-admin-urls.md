---
schema: 1
kind: pr
pr: 911
title: Specs 53.16 and 53.17 name the operator console's URLs (admin.maktaba.tv)
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [852]
---

## What happened
- Stories 53.16 and 53.17 named `app.maktaba.tv/admin/reports`, `/admin/errors` and `web/cloud/src/pages/admin/`. The service admin's views moved with the operator console (story 50.32) to `admin.maktaba.tv` (`web/admin/`), and #852 brought 53.17's pages along. The specs now name the console.
- No Arabic to change: `docs/wiki/db/ar/epic-53.json` translates each story's summary only, and none of the changed lines are in those summaries. `generate_wiki.py --check` passed (1431 of 1431), and regenerating changed nothing.

## What we learned
- An edit to a spec's body changes the Arabic only when it falls inside the wiki entry's text. Run `generate_wiki.py --check` to see which entries a spec edit reaches.

## Open
- None.
