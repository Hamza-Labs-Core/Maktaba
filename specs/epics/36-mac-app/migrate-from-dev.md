# Runbook — move the home Mac into Maktaba.app (story 36.9)

`maktaba-server migrate-from-dev` moves a Mac that runs the developer stack
(`tools/dev/stack.sh` and the compose Postgres in Docker, `maktaba-postgres-1`) into
Maktaba.app. Run it with the app's own binary, with Maktaba installed but **quit**:

```sh
S="/Applications/Maktaba.app/Contents/Resources/server/bin/maktaba-server"
"$S" migrate-from-dev --dry-run     # the plan with sizes; changes nothing
"$S" migrate-from-dev               # asks, then moves
# open Maktaba, then:
"$S" migrate-from-dev --verify      # API up, counts kept, Live TV host checked in
"$S" migrate-from-dev --finalize    # only when sure: deletes the container and its volume
```

## What a move does

| # | Step | Notes |
|---|---|---|
| 1 | build the pipeline venv | track B's `venv_bootstrap` from the payload, while the stack still runs (minutes the first time) |
| 2 | stop the stack | `zsh <checkout>/tools/dev/stack.sh stop`; waits until the processes are gone and ports 8080/8081 are free |
| 3 | dump | first the other login roles (the machines that joined) go `NOLOGIN` and every other session on the database ends, so nothing is written after the dump. Then one read-only snapshot: the counts and `pg_dump -Fc --snapshot` see the same rows, so a joined machine that still writes cannot skew the check. The dump is `backups/from-dev-<time>.dump` and is kept |
| 4 | stop the container | `docker stop` (the volume is kept): joined machines cannot write to the old database any more |
| 5 | restore | a fresh `maktaba` database in the bundled Postgres; the extensions are created by its superuser, everything else is restored with `--role=maktaba --no-owner`, so the app role owns it. The source's other roles (`maktaba_worker`, the machines' roles) are created `NOLOGIN`, so the grants restore. The counts must match exactly |
| 6 | copy | `secrets.key` (`~/.config/maktaba`), `stream-signing.key`, the Chroma index, the streaming cache, the Hugging Face repos the pipeline loads (Whisper, the second listen, the embedder) and the HF token, CAMeL Tools data, the Ollama models (`~/.ollama/models` or `OLLAMA_MODELS` → `models/ollama`, the language model of the LLM stages). APFS clones: no extra space. A key file already in the app's place is never replaced; other folders are replaced only with `--replace` |
| 7 | server.toml | `[media] path_map` and the STT settings (`workers`, `audio_hours`, `gpu_gb`, `gpu_cache_gb`, `min_free_pct`, the models) from the running stack (`ps -E`), else the shell, else stack.sh's defaults. Never a `[database] url`: the bundled Postgres is used |

The settings and the checkout come from the running processes, so run it while the stack runs.
`--dev-repo`, `--container` and `--payload` override what it finds; `--replace` goes ahead over an
app install that has a `server.toml` or a database already.

The move is recorded in `~/Library/Application Support/Maktaba/migrate-from-dev.json`
(`dumped` → `migrated` → `verified` → `finalized`). The restore's log is
`~/Library/Logs/Maktaba/migrate-from-dev.log`.

## Going back

Until `--finalize`, nothing of the stack is deleted:

1. quit Maktaba (or `launchctl bootout gui/$(id -u)/com.hamzalabs.maktaba.server`)
2. `docker start maktaba-postgres-1`
3. log the joined machines in again (the run prints the exact `ALTER ROLE … LOGIN` line)
4. `cd <checkout> && CGO_ENABLED=0 zsh tools/dev/stack.sh start 2`

What changed in the app after the move stays in the app's database, not in the stack's.
`--replace` dumps an app database that has data into `backups/maktaba-<time>.dump` before replacing it.
`--finalize` needs the move verified, the dump still in `backups/`, and Maktaba running (it checks
once more), then asks before it deletes the container and its volume.

## Known gaps

- **Joined machines** reach the home's database on port 5432. The bundled Postgres listens on
  this Mac only, so they stop working after the move until that is opened up (the dry run names them).
- `MAKTABA_EMBED_MODEL` has no server.toml setting; the dry run warns when the stack sets it.
- `setup --json` has only the `status` step: enough for the app to adopt a moved Mac
  (`configured: true` opens the library instead of the setup screens).
