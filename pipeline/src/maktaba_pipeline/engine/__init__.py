"""The queue graph engine (plan 06-11).

Change-driven, multi-level job queues on top of ``processing_jobs``:

* :mod:`.graph` - the stage graph, single source of truth;
* :mod:`.enqueue` - the one way to queue a job (coalescing, debounce, re-run);
* :mod:`.claim` - pool-aware claiming;
* :mod:`.run` / :mod:`.propagate` - skip check, handler, change propagation;
* :mod:`.events`, :mod:`.causes`, :mod:`.health`, :mod:`.workers` - visibility.

The package ``__init__`` stays empty of imports on purpose: ``db.jobs`` imports
the engine lazily and the engine imports ``db.jobs``.
"""
