### Fixed

- **A library stopped hard and started again at once no longer fails its database's move to
  PostgreSQL 18.** The move first starts the old database to recover it. After a power cut or a
  forced stop, that start could meet memory the old processes still held ("pre-existing shared
  memory block is still in use"): the move failed, and the home stayed on PostgreSQL 16 until its
  next start. It now waits until the memory is free. So does every other start the program makes of
  its own database (a restore, a moved library's check), and on Windows its everyday start too.
