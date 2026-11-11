---
schema: 1
kind: pr
pr: 1090
title: "Story 59.01: the server image runs as any uid:gid, never root, first start offline"
date: 2026-11-10
session: 06bd298e-84a0-42e2-8995-650ae07ec232
agent: claude-code
---

## What happened
- Merged as 2228aa84b. `maktaba-server container` runs a preflight (`container_preflight.go`)
  before anything else:
  - it refuses uid 0, a privileged container and the Docker socket;
  - it checks that the volume takes a real write; a refusal exits 78 with English then Arabic;
  - it names a uid that `/etc/passwd` lacks through `nss_wrapper`, writing `<volume>/.nss` and
    preloading `libnss_wrapper`.
- The privileged check needs `CapBnd` with `CAP_SYS_ADMIN` and `/sys` mounted `rw`, both. With
  both required, `--cap-add SYS_ADMIN` alone is allowed.
- The Linux payload carries `pipeline/wheels`: every pin of `constraints-core.txt` for its
  architecture, plus `wheelhouse.json` naming the constraints sha256 it covers.
  - `venv_bootstrap` adds `--no-index --find-links` when the wheelhouse covers the
    constraints, and `--find-links` beside PyPI otherwise.
  - `check.sh` builds the core venv from it against an index that answers nothing.
- Setup's model download is skipped when the chosen components hold no `model.*` (a Core
  home). Without that, an offline Core setup failed on the Whisper download.
- CI: `tools/release/linux-app/docker-nas.sh` in the Linux app job, on both architectures.
  - 1000, 568:568, 99:100, 1026:100 and 4321:4321 each run setup with `--network none`, then the
    container offline. All four uids and gids of every process are checked.
  - The refusals are checked, and so is an estimate of the compressed size (355–390 MB).
  - `release.yml` checks the pushed manifest's layers per architecture against 1 GB.

## What we learned
- Children already inherit the entrypoint's environment (`os.Environ()` plus the role's
  variables), so `LD_PRELOAD` and `NSS_WRAPPER_*` reach `initdb` and `postgres` with no
  `pgbundle` change. Only `appvenv.bootstrapEnv` allow-lists variables; the three were added
  there.
- A fresh named volume mounted at `/var/lib/maktaba` takes the image's folder ownership (1000)
  at its first mount. To test a root-owned volume, first mount it at that path, then chown it.
- Do not `chmod -R` in the Dockerfile after `COPY`: it copies the whole payload into a second
  layer. `payload.sh` already sets the modes, so the Dockerfile only checks them (an empty
  layer).
- A local loop with no Linux build works: unpack a released payload, swap in a
  cross-compiled `maktaba-server`, patch `venv_bootstrap.py` into the wheel (and its RECORD),
  regenerate `files.sha256`/`payload.json` with `payload_tool.py manifest` in a Debian
  container, and build the image.
- `venv_bootstrap` progress goes to stdout JSON only, so `venv.log` was empty. It now also
  writes where its packages come from to stderr.
- Problems on the way:
  - `lint / repo` failed on `test_no_head_pipes.py`: the new script piped `sed` into
    `grep -q`. Fixed by testing the line in bash.
  - `load-small` (and `cloud-rehearsal`, its rollup) failed once with "the relay got more than
    20% worse". This PR does not touch the relay, and it passed on the next head.

## Keep doing
- Run a new image check locally on Docker before CI. Every uid shape passed locally first.

## Stop doing
- Piping into an early-exit reader (`grep -q`, `head`) in scripts under pipefail.

## Open
- The setup text still says the venv step downloads about 2 GB, even for an offline Core
  home (a web setup string, story 59.14).
- 59.15's nas-shapes job can reuse `docker-nas.sh`. A Full home (speech to text on) still needs
  PyPI for its ML wheels until 59.06's `runtime.ml` pack.
