### Fixed

- **A restore that cannot finish always has a safe way out.** When putting a backup back failed (the
  backup lost or damaged, a full disk), the library stayed stopped on it, whatever you did. The
  start page and the recovery page now say what the library database is: as it was, holding a part
  of the backup, or gone. You can try again, put another backup back, or, while nothing was changed,
  give the restore up (the recovery page, or `maktaba-server restore --abandon --confirm`): Maktaba
  runs again on the library as it was before it. A newer release installed meanwhile goes on from
  there too. A restore that hangs can be stopped. The library as it is now is set aside first, once,
  and no backup is removed until the restore is done but to make room for that copy. With no room for
  it the restore stops before it touches anything and says the space it needs; it goes on without a
  copy only when you agree to that. A backup that an older version puts back is asked of it only when
  that version has room for its own copy, and what the restore needs is kept out of its reach. In
  English and Arabic.
