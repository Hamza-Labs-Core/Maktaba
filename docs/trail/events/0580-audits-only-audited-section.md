---
schema: 1
kind: pr
pr: 580
title: Player UI accessibility audits timed out: lay out only the audited section
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 302453bdb. Audits failed with "Audit failed to complete in time" or no snapshot after the audit.
- Cause: `.textClipped` lays the app out again at all 12 Dynamic Type sizes within the audit's about 15 s limit (10 of 16 s; leaving out `.dynamicType` never stopped that sweep), and with all tabs showing, UIKit's tab bar took about 40 % of each relayout (2.0 s vs 1.1 s per check).
- Libraries' audit now launches the section alone, and `loadEveryPage` counts only the current launch's requests, so grid page c2 no longer loads mid-audit.
- Every audit prints its time, and sim-settle logs the runner's busiest processes (iPhone 2/6 ran at 0 % idle all job).

## What we learned
- The `.textClipped` audit relays the app at all 12 Dynamic Type sizes regardless of `.dynamicType`; the more UI on screen, the closer it gets to the audit's time limit.
