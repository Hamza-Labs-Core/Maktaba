---
schema: 1
kind: pr
pr: 1754
title: "A refused machine action says its own reason; going past a copy is audited when it happens"
date: 2026-11-25
session: af4db1a0-1123-496c-869f-9a21bd3c8aad
agent: claude-code
prs: [1749, 1732]
---

## What happened
- **#1749's review had six minors, one of them false words:** the Updates page worded the code `no_update` as "There is no verified release to install", and the API answered that code for a home that does not run the release, for a withdrawn one and for a machine's own refusal. Each has its code now.
- **Corrections of #1749's event.** "Waits before its restart" is before the home applies the release: its backup comes in between, and a copy that begins during it is ended all the same. The row that says a run went past a copy was written at each try; the one that says the restart did was missing when viewers came during the wait.
- **The live home's Postgres panicked on an interrupted `open()` of a WAL file (#1751),** at a load over 60. PostgreSQL 18.7 and master do not try `open()` again on EINTR, and no setting helps. It had met the same on another file on 2026-11-20, for one connection only.

## What we learned
- **Words for a code are words for every use of the code.** Before wording one, look for every place that answers it.
- **A test's fake answers what the code expects.** The machine's refusal was tested with a plain sentence; a real machine answers `{"error": …, "status": …}`, and the page showed that JSON. Write the fake from the code that answers.
- **No answer is not a refusal.** A session that ends with a command under way may have carried it out: say that, and not that the machine refused.
- **An audit row of something that happens goes after it happened,** and one that says "once" needs a mark of its own: a state other waits write over (`waiting`) does not keep it.
- **macOS has 32 System V shared-memory segments** (`kern.sysv.shmmni`). A Postgres server killed by a test leaves its own; when all are taken `initdb` fails, and so would the extra servers a home's update starts. `ipcs -m -p` names each one's creator.
- **A shallow clone makes `trail check` say a snapshot's "Missing events" is wrong,** since the history it reads is cut. CI's check is right.

## Stop doing
- **No native Postgres server and no update rehearsal on the Mac of the live home:** Docker with a CPU limit, or CI.

## Open
- #1753 (the hold's words on the web and Android, and 17 from v0.3.162 too), #1751, #1667.
