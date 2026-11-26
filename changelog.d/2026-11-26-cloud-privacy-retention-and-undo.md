### Security

- **Cloud security records expire.** Audit records expire after 90 days and abuse signals after
  30 days. Deleting an account also removes security records linked directly to that account
  or its homes.

### Fixed

- **Cloud rollback after a migration backfill.** A failed deploy can roll back a migration
  whose backfill changes only columns that the migration adds and its Down script drops.
