### Fixed

- **Search index migration.** The Chroma copy skips superseded transcripts, verifies vectors
  against surviving segments, and reports damaged source files in Processes. Joined machines
  wait for the copy before queueing search index repairs.
