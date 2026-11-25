### Fixed

- **Failed items come back by themselves once what made them fail has passed.** A file that could
  not be found and is there again, a NAS that stayed busy for longer than a step's tries, or a
  database that went away while a step ran, is queued again behind all other work, without a scan
  and without pressing **Retry**. Posters that failed on a share that was opened under the wrong
  folder come back this way. A file that is still gone is looked for less and less often, and an
  item that keeps failing is brought back three times at most.
