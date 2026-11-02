# maktaba-pipeline

The transcription and indexing pipeline: scanner, audio, transcription, subtitles,
search, queue.

## Running the tests

The whole suite, as CI runs it (CI installs every extra: `uv sync --frozen --all-extras`):

```sh
cd pipeline
MAKTABA_TEST_PG_URL=postgresql://postgres@127.0.0.1:5433/postgres \
  uv run --frozen --all-extras pytest
```

`MAKTABA_TEST_PG_URL` points at a scratch Postgres server the tests may create and drop
databases on (never the live one). It needs the `pg_trgm` and `vector` extensions and a
UTF8 database (`initdb -E UTF8`): a `SQL_ASCII` server fails the tests that store Arabic
text in JSON. Without the variable the `[postgres]` cases are skipped.

`uv run --frozen --extra dev pytest` runs the same suite without the stage extras (`stt`,
`search`, `mediaintel`). The `dev` extra brings numpy, which the tests use directly, so
nothing fails for want of it; tests that need an extra's own libraries (chromadb,
sentence-transformers, mlx, faster-whisper) are skipped, and the stages that need them
are checked to be left unserved. Use `--all-extras` before saying the suite passes.
