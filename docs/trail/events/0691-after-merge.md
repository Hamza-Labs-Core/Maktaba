---
schema: 1
kind: pr
pr: 691
title: After the merge of #691 — a crash envelope's item length
date: 2026-11-07
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- CodeQL (`go/uncontrolled-allocation-size`) flagged `parseEnvelope`: it allocated each item's payload from the `length` its header claims, a number the client sends. A length past the envelope is now refused (400) before any allocation, with a test that claims 2 GB.

## What we learned
- In a length-prefixed format, the length is the sender's word: bound it by the bytes actually received before allocating.
