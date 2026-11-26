### Fixed

- **A restore on your own PostgreSQL is refused before anything is touched when the backup cannot be
  put back in the restore's one transaction** — large objects, an event trigger, a foreign server or
  its wrapper, a view or a table in a schema of the user's own, grants matched only by a name, a
  zero-argument aggregate, or a bundled home's dump restored on another server. Each of these used to
  fail part way, saying "nothing changed" though it had changed things, or to be found not whole;
  now the plan says what the backup holds, and nothing is dropped or ended. Without `psql` beside
  `pg_restore` the restore is refused before the copy slots go too.
- **A stop between asking for a restore and the switch to the release it is for no longer puts the
  backup back and migrates it in the same breath.** The newer release that is still the one to start
  leaves the restore for that release's own start and says so; it can be given up from the recovery
  page, and the library goes on as it is.
- **The copy a killed try of a restore took is found, not taken again.** The restore names its copy of
  the database in the record before it takes it, so a later start reuses the dump a kill left between
  the two, and the room for a copy is not asked for when one is there already (found where an older
  release left it, too).
- **A private PostgreSQL start no longer waits out shared memory another server took over**, and the
  supervisor starts a start again at once when it ends on the shared memory a killed postmaster left,
  instead of backing off as a crash. A stop in the middle of a major upgrade's putting back of the old
  cluster's grants is bounded too, and a Windows path outside the ANSI code page no longer misses the
  lock.
