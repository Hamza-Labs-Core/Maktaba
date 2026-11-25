### Fixed

- **A database restore that is cut off, or meets a damaged backup, leaves the library as it was.**
  On a library with its own Postgres server, a restore killed at the wrong moment, or one from a
  backup whose rows were cut short, could put part of the backup over the library and report
  success. The restore is now one transaction that ends only once every table's rows were counted.
  A backup that cannot be read, or whose files for search by meaning are damaged, is refused before
  anything is touched, with Maktaba's own database too.
