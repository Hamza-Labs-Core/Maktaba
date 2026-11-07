---
schema: 1
kind: pr
pr: 864
title: The api tests' fake servers ignore a stray request
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- `TestSenderRetriesAndRefusals` failed on #810. A `GET /healthz` reached the fake cloud's 127.0.0.1 port, probably another process probing a recycled port, and the fake counted it as an envelope.
- The telemetry fakes now take only `POST IngestPath`. Anything else gets a 404 and is neither recorded nor an error.
- Thirteen more fakes in `api/` (in eleven files) counted or recorded every request. Each now takes only the request its code sends. The three supervisor fakes count a call only once it carries the API's token.

## What we learned
- An `httptest` server listens on a port the kernel may have given another process moments before. On a shared CI runner something can probe it, so a fake that counts every request turns a stray request into a test failure.

## Keep doing
- A fake server checks the method and path first. It answers 404 to anything else and counts nothing before that check.

## Open
- Nothing.
