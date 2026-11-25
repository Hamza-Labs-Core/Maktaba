---
schema: 1
kind: pr
pr: 1732
title: "A backup is put back inside public when the database's user may not make it again"
date: 2026-11-25
session: af4db1a0-1123-496c-869f-9a21bd3c8aad
agent: claude-code
prs: [1727, 1670]
---

## What happened
- **#1720:** on an operator's own Postgres whose user is no superuser, with pgvector in `public`, no backup could be put back, and the rehearsal failed. The restore now empties `public` in its one transaction, keeps the schema and the extensions, and leaves the dump's items for them out. What cannot work is refused up front, when the restore is asked too.
- **The identity of the data** was the cluster, the database and the oid of `public`. A restore that keeps `public` would have read as "untouched", with giving up offered on the backup's data. The migrations' own table (its oid) is part of it now, in a field of its own.
- **The rehearsal is skipped with its reason.** A scratch database made from template0 has no extension to keep, so it cannot use the same path.
- **A release says what its restore can do** (`release.json`'s `restore_protocol`).
- **The review's major: a whole, committed restore was recorded as failed, on every try.** A dump holds grants on an extension's own functions where someone made any (migration 0103's, made when the user was a superuser). A user who does not own them gets a warning only, so the restore committed, and the check of it, by the dump's full list, missed them: "not whole", the home stopped, giving up refused. Those items, and other users' default privileges, are left out of what is restored, and the check goes by that list.

## What we learned
- **GRANT on what is another's is a warning, not an error** (as long as the user has some privilege on it). A check after the commit that counts grants must count only those that could be made.
- **What a dump grants on and does not define is an extension's.** pg_dump writes an extension's objects not at all, but the grants on them that were changed: a rule that needs no look at the database the dump came from. A function's grant names its arguments another way than its definition does, so compare the name.
- **CASCADE goes past the schema.** A view, a column or a partition of another user in another schema goes with what is in `public`, silently. The same walk over `pg_depend` (what depends, and what that is a part of) says it before; a range type's own functions are the first superuser's, and no one's loss.
- **To know whether a user may make an extension, make it and roll back.** A control file's `superuser` is not what a hosted server does.
- **CI's Postgres is another one.** Its 16 has no pgvector, so its dumps have no chunks and another refusal comes first: a copy of the local 16 with pgvector's files taken out shows it in minutes.
- **What depends on a schema is in `pg_depend`:** the rows with `refclassid = pg_namespace` and deptype `n` are exactly what `DROP SCHEMA … CASCADE` drops first. Leave out those with deptype `e` (an extension's) and `i` (another object's own part), and drop each with `IF EXISTS … CASCADE`, the statements made before the first one runs.
- **A partitioned table has an internal dependency on itself** (its key's columns): the rule for `i` left it out. Only the comparison of `pg_dump --schema-only` before and after showed it; a test of the things one thinks of had passed.
- **Default privileges must go first.** A dump writes a table's grants only where they differ from the default, so a default privilege left in place adds its grants to every table put back, those a migration took away too.
- **Change how a thing is done, then look for what proves it was done.** The identity was designed on "every restore makes another schema"; this change made that false, far from the code that relied on it.
- **A new field of a shared record is apart from the old one,** not a longer value of it: a release that compares the whole value would have taken its own database for another, and set no copy aside. A release that drops the new field, and goes by the old one, is told by a mark on the old one, made before the step the field is about.
- **Staged releases hold only the migrations** (`ExtractMigrations` refuses all else), so a marker in `migrations/meta/` is in the apps' payloads only. The signed `release.json` is in both kinds that matter.
- **An owner with no row in `pg_shdepend` is the first superuser** (its objects are pinned).
- **`pg_terminate_backend` in one statement stops at the first session it may not end:** a superuser's idle session kept all of the user's own.

## Keep doing
- Compare the whole catalog (`pg_dump --schema-only`, less its random `\restrict` word) when a test says "as it was".

## Open
- A hosted Postgres was not run. The review's other minors: #1736.
