---
schema: 1
kind: pr
pr: 1698
title: "A killed or damaged restore on an own Postgres commits nothing"
date: 2026-11-25
session: af4db1a0-1123-496c-869f-9a21bd3c8aad
agent: claude-code
prs: [1670]
---

## What happened
- The external restore (psql, a Postgres the home does not bundle) used `--single-transaction`, which commits at the end of its input. A server killed part way closed that input: the part sent was committed over the library. Found while #1670 was written.
- The script now opens its own transaction, and its last line is the COMMIT. Before it: the COPY blocks seen whole in the script, every table's rows counted against the dump's. Before anything is sent: the dump listed, its counts and its chunk files checked.

## What we learned
- **psql's `--single-transaction` is about psql's errors, not its caller's death.** End of input is a commit.
- **PostgreSQL 18's `pg_restore` exits 0 on a table file cut short.** Its rows' `\.` line is in the dump's file, so psql then reads the following SQL as rows, the COMMIT too.
- **A guard written as SQL does not work:** psql 16.15 stays in COPY mode when the statement after it is empty or a syntax error. Reading the script in Go does.
- **`pg_restore --section=data` alone gives grants before the chunked rows;** pre-data and data have to be one run for the grants to come after.
- **A dump with no `counts.json` exists in the field** (v0.3.37 to v0.3.42). Every check has to hold without it; the chunks' own trailers do.
- **`exec.Cmd.Wait` waits for the pipe, not the process,** when a wrapper script's child keeps it. `WaitDelay` bounds that, in product code and in tests.
- **A test that swallows as much as it adds proves nothing:** the COPY inside a function's text was read as one table more and then ate the real one's end. Adding a `\.` to the text makes it fail without the guard.

## Keep doing
- Make old dumps with the old release's real code (a worktree at its tag) before saying a new check refuses none of them.

## Open
- Large objects in a dump, the wording after COMMIT was sent, a last chunk gone without counts, the wording when there is no psql and the `REVOKE` the new public schema leaves: #1688.
