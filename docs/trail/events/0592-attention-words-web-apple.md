---
schema: 1
kind: pr
pr: 592
title: Needs attention titles in English and Arabic on web and Apple Remote
date: 2026-11-03
session: unknown
source: claude-trail.md
prs: [590]
---

## What happened
- Merge 646dee3f6 carries #590's "Needs attention" fix to the web queue page and Maktaba Remote for iPhone/iPad.
- Both title a failure group by its cause code, or a generated cause's kind, in English or Arabic, using Android's exact words stored under `queue.issues.cause.<code>` in en.json, ar.json and MaktabaKit's catalog. The home's English label is used only for a code the app has no words for, and never when it is a grouping key with `<n>`/`<path>`.
- A group's sample message shows its escaped line breaks as real lines, with Arabic path parts bidi-isolated as in AttentionWords.kt.
- Logic in `web/src/lib/attentionWords.ts` and `MaktabaKit/AttentionWords.swift`, with web and MaktabaKit unit tests for English, Arabic and the fallback. QOV-13's crash test now uses an unmapped cause, since known causes no longer render the server label.
