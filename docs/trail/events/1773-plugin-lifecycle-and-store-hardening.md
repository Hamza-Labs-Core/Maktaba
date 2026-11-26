---
schema: 1
kind: pr
pr: 1773
title: First-party plugin lifecycle and Store package hardening
date: 2026-11-26
session: 01a2189e-a6eb-7a72-bbcd-d86940c29fbc
agent: codex
---

## What happened
- [PR #1773](https://github.com/Hamza-Labs-Core/Maktaba/pull/1773) merged as `c6c9a44a946fe76568b37c64403696290160b99e`, closing [#1463](https://github.com/Hamza-Labs-Core/Maktaba/issues/1463), [#1458](https://github.com/Hamza-Labs-Core/Maktaba/issues/1458), and [#1456](https://github.com/Hamza-Labs-Core/Maktaba/issues/1456).
- The batch extended lifecycle coverage across API and pipeline components, tightened package JSON parsing and signer provenance checks, and hardened Store publisher moves and audit delivery.

## What we learned
- Package validation checks for duplicate keys at every JSON object depth and preserves exact number tokens during parsing.
- Publisher ownership can change during countersigning. Publication checks the publisher guard again and preserves shared R2 blobs if that guard fails. Catalog audit events are recorded in an outbox for delivery to the accounts database.
