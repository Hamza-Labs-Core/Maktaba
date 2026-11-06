---
schema: 1
kind: pr
pr: 624
title: Removals and toggles made while a list is read again are kept: ReadOrder
date: 2026-11-03
session: unknown
source: claude-trail.md
prs: [616]
---

## What happened
- Merge 2f148c602. The race #616 fixed for toggles was also in removals (History Forget and Load more, ending a session, recent searches, Not interested, library pins on Apple and Android, and many web admin, queue and search pages): a read that began before the save brought the removed row or the old state back.
- Each platform has one `ReadOrder` (web `lib/readOrder.ts`, MaktabaKit `ReadOrder.swift`, Android player `ReadOrder.kt`). A read's answer applies only if newer than every change and every answer shown, and saves run through `saving()`.
- Each fix has a latch test that failed without it. Two flaky tests were fixed at the cause (iPad quality reopen, Android posture layout).
