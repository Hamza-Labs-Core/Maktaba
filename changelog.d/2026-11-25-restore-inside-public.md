### Fixed

- **A library on your own PostgreSQL can put a backup back when its user is no superuser.** With
  pgvector in the `public` schema, made by a superuser, every restore stopped with "permission
  denied to create extension". The backup is now put back inside the schema: the extensions stay, and
  everything else in it is the backup's, in one transaction. When that cannot work it is refused as
  you ask for it, with the reason. An update is no longer held up because its trial run on a copy
  could not make the extension: the trial run is skipped, and says so.
