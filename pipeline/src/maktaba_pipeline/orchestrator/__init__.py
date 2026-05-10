"""Pipeline orchestrator — owns the state-machine mutator.

:func:`maktaba_pipeline.orchestrator.advance.advance_after_stage` is
the SOLE function that issues ``UPDATE videos SET state = …``. All
callers — stage workers, the filesystem watcher, library merge logic,
the integrity sweeper — funnel through it.
"""
