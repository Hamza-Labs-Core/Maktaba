# The fault rehearsal

> Requirement (Epic 53, story 53.07): one crash never takes the web app down, and every
> failure path is proven in CI before a release, as the update rehearsal proves updates.

The fault rehearsal runs a home of **this commit**, built and seeded by the update rehearsal's
kit (`tools/rehearsal/build.sh`, the small seed; see [update-rehearsal.md](update-rehearsal.md)),
started the way the Mac app starts it (`maktaba-launcher serve` from the app's bundle), and
breaks one part at a time. Its scenarios are jobs of `update-rehearsal.yml` (ids `F03`, …), so
the tag guard, which refuses a tag unless the update rehearsal passed on that exact commit,
requires them too. Each one ends with the home serving again (`assertServing`), and keeps the
machine's logs (the supervisor's segments under `<data>/logs/segments/` among them) as an
artifact when it fails.

The tests are `api/rehearsal_faults_e2e_test.go`. They read the home's `/status.json`
([api/status.md](api/status.md)) every 250 ms and time the web shell's first answer.

## The scenarios

| # | Fault | Asserts | Budget |
|---|---|---|---|
| F03 | `kill -9` streaming | the shell answers in ≤ 1 s; `/status.json` says streaming is restarting or down; it is back; its lines are in its own segments (story 53.02) | status ≤ 15 s; back ≤ 60 s |
| F04 | `kill -9` Postgres | the shell answers; `database` not ok; the recovery console opens with the admin's recovery pass and lists Postgres; without a pass it is 401 | console ≤ 10 s |
| F06 | `kill -9` the API | the shell answers; `api` restarting or down and `overall: down`; it is back | status ≤ 5 s; back ≤ 30 s |
| F10 | the console's **Start in safe mode** | the home restarts with `mode: safe`, processing and streaming `off`, the API `ok`; the flag file is gone (one start); **Leave safe mode** brings it back | — |

## What it does not do yet

- The status strip's text in a real browser and the player's sentence (story 53.06): the strip's
  rules are unit-tested on every platform; a browser check joins when the rehearsal builds the
  web in (`--with-web`).
- F01, F02, F05, F07–F09 and F11–F14 of the story: the cut-offs, the launcher's can't-start page
  (F09) and the disk floors (F11) are unit-tested (`cmd/maktaba-server/internal/supervisor`,
  `cmd/maktaba-launcher/cantstart_test.go`, `internal/logkeeper`); the log index (F08) comes with
  story 53.09.

## Running it by hand

As the update rehearsal (its kit and seed), then one scenario:

```bash
MAKTABA_E2E_REHEARSAL=1 MAKTABA_REHEARSAL_KIT=$KIT MAKTABA_REHEARSAL_PG_HOME=$PG \
  go test -count=1 -run TestFaultRehearsalF06APICrash -v ./api
```

Never against a live install.
