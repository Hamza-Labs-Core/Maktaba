---
schema: 1
kind: pr
pr: 870
title: The hub's queues/library test waits for the library line
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- `TestQueuesWorkersLibraryTopics` failed with "library line missing" on #847, which changes only Python. The hub's file writer takes records from a channel. The queues and library loops each write a line at start, in either order. The test waited for the queue line, then counted the library line once without waiting.
- The test now waits, flushing, for each line it reads, and reads every day folder of a kind.

## What we learned
- Seeing one record of an asynchronous writer says nothing about a record another goroutine queued at about the same time. Wait for each record a test reads.

## Keep doing
- A test reads a writer's dated folders by glob, or by the writer's own clock, never by a clock of its own.

## Open
- Nothing.
