### Fixed

- **Putting a backup back on your own PostgreSQL 18 works when it has pg_textsearch.** The restore
  stopped with "schema textsearch already exists", with nothing changed: no backup of that library
  could be put back. Every role also keeps the use of the `public` schema, and without `psql` a
  restore that could not finish is refused before anything is touched.
- **A restore that is not done goes on after the backups are moved**, and a second backup asked for
  lets go of the first, so deleting it frees its space.
- **Updates warns before an older version puts a backup back:** that version's own rules may remove
  other backups.
